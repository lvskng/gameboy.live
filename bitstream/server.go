package bitstream

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"

	"golang.org/x/time/rate"

	"github.com/HFO4/gbc-in-cloud/gb"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type BitstreamSound struct{}

func (b BitstreamSound) Init() {
	return
}

func (b BitstreamSound) Play() {
	return
}

func (b BitstreamSound) Trigger(address uint16, val byte, vram []byte) {
	return
}

type Server struct {
	Core *gb.Core

	pixels    *[160][144]uint8
	pixelLock sync.RWMutex

	drawSignal chan bool

	connections         map[string]*Connection
	connectionsLock     sync.Mutex
	newConnectionSignal chan bool
	inputs              map[string]byte
	inputLock           sync.Mutex
	inputStatus         *byte
	lastInput           byte

	Config                  BitstreamServerConfig
	SubscriberMessageBuffer int

	rateLimiter *rate.Limiter

	Debug bool

	inputChannel chan struct {
		id  string
		msg []byte
	}

	dropChannel chan string
}

type BitstreamServerConfig struct {
	Port                int    `yaml:"port"`
	GamePath            string `yaml:"game_path"`
	WebSocketUrl        string `yaml:"websocket_url"`
	Url                 string `yaml:"url"`
	FullPictureInterval int    `yaml:"full_picture_interval,omitempty"`
	Debug               bool   `yaml:"debug,omitempty"`
	ClientWriteTimeout  int    `yaml:"client_write_timeout"`
	PauseIfIdle         bool   `yaml:"pause_if_idle"`
	RateLimit           struct {
		ms    int `yaml:"ms"`
		burst int `yaml:"burst"`
	} `yaml:"rate_limit,omitempty"`
}

type PageData struct {
	ServerURL    string
	GameName     string
	DebugMin     string
	WebSocketUrl string
}

type Connection struct {
	c         *websocket.Conn
	l         *sync.Mutex
	m         chan []byte
	closeSlow func()
	close     func()
	closed    *bool
}

func (s *Server) Run(sig chan bool, f func()) {
	panic("implement me")
}

// Run Running the static-image gaming server
func (s *Server) InitServer() {
	// startup the emulator
	core := &gb.Core{
		FPS:           60,
		Clock:         4194304,
		Debug:         false,
		DisplayDriver: s,
		Controller:    s,
		DrawSignal:    make(chan bool),
		SpeedMultiple: 0,
		ToggleSound:   false,
		UseRGB:        false,
		PauseSignal:   make(chan bool),
		Sound:         BitstreamSound{},
	}
	s.Core = core
	s.drawSignal = core.DrawSignal
	core.Init(s.Config.GamePath)
	go core.Run()

	s.rateLimiter = rate.NewLimiter(rate.Every(time.Duration(s.Config.RateLimit.ms)), s.Config.RateLimit.burst)
	s.SubscriberMessageBuffer = 16
	s.connections = make(map[string]*Connection)
	s.inputs = make(map[string]byte)
	s.newConnectionSignal = make(chan bool)
	s.inputChannel = make(chan struct {
		id  string
		msg []byte
	})
	s.lastInput = 0xFF

	s.dropChannel = make(chan string)
	go s.dropConnections()

	http.HandleFunc("/stream", s.handleSubscribe)
	fs := http.FileServer(http.Dir("ws-client/static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check if the request is for index.html
		req := strings.ToLower(r.URL.Path)
		if req == "/" {
			req += "index.html"
		}
		// Parse the HTML template
		tmpl, err := template.ParseFiles("ws-client" + req)
		if err != nil {
			log.Printf("Error parsing template: %+v", err)
			w.WriteHeader(404)
			return
		}

		debugmin := "min."
		if s.Debug {
			debugmin = ""
		}

		// Create page data with the server URL
		data := PageData{
			ServerURL:    s.Config.Url,
			GameName:     core.GameTitle,
			DebugMin:     debugmin,
			WebSocketUrl: s.Config.WebSocketUrl,
		}
		w.Header().Add("Access-Control-Allow-Origin", s.Config.Url)
		// Execute the template with the data and serve
		err = tmpl.Execute(w, data)
		if err != nil {
			log.Println("Error executing template:", err)
			w.WriteHeader(500)
		}
	})
	go s.serveData()
	go s.handleInput()
	err := http.ListenAndServe(fmt.Sprintf(":%d", s.Config.Port), nil)
	if err != nil {
		log.Fatalf("Error creating webserver: %+v", err)
	}
	//ticker := time.NewTicker(10 * time.Minute)
	//for {
	//	select {
	//	case <-ticker.C:
	//		log.Printf("[Bitstream] Starting closed connection garbage collector")
	//		s.connectionsLock.Lock()
	//		s.inputLock.Lock()
	//		for id, conn := range s.connections {
	//			if *conn.closed {
	//				log.Printf("[Bitstream] Found closed connection %s", id)
	//				delete(s.connections, id)
	//				delete(s.inputs, id)
	//			}
	//		}
	//		s.connectionsLock.Unlock()
	//		s.inputLock.Unlock()
	//	}
	//}
}

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	id := uuid.New().String()
	ipAddr := r.Header.Get("X-Real-Ip")
	if ipAddr == "" {
		ipAddr = r.Header.Get("X-Forwarded-For")
	}
	if ipAddr == "" {
		ipAddr = r.RemoteAddr
	}
	log.Printf("[Bitstream] New connection %s from %s", id, ipAddr)
	err := s.subscribe(r.Context(), w, r, id)
	if errors.Is(err, context.Canceled) {
		return
	}
	if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
		websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return
	}
	if err != nil {
		log.Printf("[Bitstream] Error with connection %s: %v", id, err)
		return
	}
}

func (s *Server) subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request, id string) error {
	var c *websocket.Conn
	mu := sync.Mutex{}
	var closed = false
	c2, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}
	mu.Lock()

	c = c2
	conn := &Connection{
		c:      c,
		l:      &mu,
		m:      make(chan []byte, s.SubscriberMessageBuffer),
		closed: &closed,
		closeSlow: func() {
			mu.Lock()
			closed = true
			err := c.Close(websocket.StatusPolicyViolation, "connection too slow")
			if err != nil {
				log.Printf("[Bitstream] Error closing WebScoket connection: %+v", err)
			}
			mu.Unlock()
			s.dropChannel <- id
		},
	}

	if closed {
		mu.Unlock()
		return net.ErrClosed
	}

	s.addConnection(conn, id)
	defer conn.closeSlow()
	mu.Unlock()
	defer func(c *websocket.Conn) {
		err := c.CloseNow()
		if err != nil {
			fmt.Printf("[Bitstream] Error closing WebScoket connection: %+v", err)
		}
	}(c)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				_, input, err := c.Read(ctx)
				if err != nil {
					return
				}
				if len(input) > 0 {
					s.inputChannel <- struct {
						id  string
						msg []byte
					}{id: id, msg: input}
				}
			}
		}
	}()
	bitmap, _ := s.GetBitmap()
	msg := []byte{0xFA}
	for _, line := range bitmap {
		msg = append(msg, line...)
	}
	err = writeTimeout(ctx, time.Duration(s.Config.ClientWriteTimeout)*time.Millisecond, conn, msg)
	if err != nil {
		return err
	}
	if s.Config.PauseIfIdle {
		s.newConnectionSignal <- true
	}
	for {
		select {
		case msg := <-conn.m:
			err := writeTimeout(ctx, time.Duration(s.Config.ClientWriteTimeout)*time.Millisecond, conn, msg)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}

}

func (s *Server) addConnection(conn *Connection, id string) {
	s.connectionsLock.Lock()
	s.inputLock.Lock()
	defer s.connectionsLock.Unlock()
	defer s.inputLock.Unlock()
	s.connections[id] = conn
	s.inputs[id] = 0xFF
	log.Printf("[Bitstream] Number of connections known: %d", len(s.connections))
}

func (s *Server) Init(px *[160][144]uint8, str string) {
	s.pixels = px
}

func (s *Server) InitRGB(px *[160][144][3]uint8, str string) {
	// s.pixelsRGB = px
	panic("implement me")
}

func writeTimeout(ctx context.Context, timeout time.Duration, conn *Connection, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	closed := *conn.closed
	if closed {
		return errors.New("connection is closed")
	}
	return conn.c.Write(ctx, websocket.MessageBinary, msg)
}

func (s *Server) serveData() {
	var bitmap [][]byte
	var lastBitmap [160][144]byte
	ticker := time.NewTicker(time.Duration(s.Config.FullPictureInterval) * time.Millisecond)
	sendFullImage := false
	running := true
	for {
		select {
		case <-s.drawSignal:
			if running {
				if sendFullImage {
					sendFullImage = false
					bitmap, lastBitmap = s.GetBitmap()
					msg := []byte{0xFA}
					for _, line := range bitmap {
						msg = append(msg, line...)
					}
					go func() {
						for _, c := range s.connections {
							err := s.rateLimiter.Wait(context.Background())
							if err != nil {
								log.Printf("Error in waiting for rate limiter: %+v", err)
							}
							select {
							case c.m <- msg:
							default:
								go c.closeSlow()
							}
						}
					}()
				} else {
					bitmap, lastBitmap = s.GetBitmapDelta(lastBitmap)
					msg := []byte{0xFB}
					empty := false
					for _, line := range bitmap {
						if line[1] != 0xFE {
							empty = false
							msg = append(msg, line...)
						} else {
							empty = true
						}
					}
					if !empty && len(msg) > 1 {
						go func() {
							for _, c := range s.connections {
								err := s.rateLimiter.Wait(context.Background())
								if err != nil {
									log.Printf("Error in waiting for rate limiter: %+v", err)
								}
								select {
								case c.m <- msg:
								default:
									go c.closeSlow()
								}
							}
						}()
					}
					if s.Config.PauseIfIdle && len(s.connections) == 0 && s.Core.Running {
						log.Println("[Bitstream] No connections open, pausing emulator")
						go func() {
							s.Core.PauseSignal <- true
						}()
						running = false
					}
				}
			}
		case <-ticker.C:
			sendFullImage = true
		case <-s.newConnectionSignal:
			if s.Config.PauseIfIdle {
				log.Println("[Bitstream] Resuming emulation")
				go func() {
					s.Core.PauseSignal <- false
				}()
				running = true
			}
		}
	}
}

func (s *Server) handleInput() {
	for input := range s.inputChannel {
		s.inputLock.Lock()
		s.inputs[input.id] = input.msg[0]
		s.inputLock.Unlock()
	}
}

func (s *Server) InitStatus(b *byte) {
	s.inputStatus = b
	s.lastInput = 0xFF //All buttons released
}

func (s *Server) UpdateInput() bool {
	s.inputLock.Lock()
	defer s.inputLock.Unlock()
	if len(s.inputs) == 0 {
		return false
	}
	poll := make(map[byte]int)
	for _, usrStatus := range s.inputs {
		poll[usrStatus]++
	}
	var mostVoted int
	var winner byte
	for usrStatus, frequency := range poll {
		if frequency > mostVoted {
			winner = usrStatus
			mostVoted = frequency
		}
	}
	s.inputs = make(map[string]byte)
	*s.inputStatus = winner
	return true
}

func (s *Server) DropConnection(id string) {
	conn := s.connections[id]
	if !*conn.closed {
		log.Printf("[Bitstream] Dropping connection with ID: %s", id)
		s.connectionsLock.Lock()
		s.inputLock.Lock()
		defer s.connectionsLock.Unlock()
		defer s.inputLock.Unlock()
		delete(s.connections, id)
		delete(s.inputs, id)
	}
}

// Goroutine to drop connections to avoid race conditions on connection closure
func (s *Server) dropConnections() {
	for id := range s.dropChannel {
		log.Printf("[Bitstream] Dropping connection with ID %s", id)
		s.connectionsLock.Lock()
		s.inputLock.Lock()
		_, ok := s.connections[id]
		if !ok {
			log.Printf("[Bitstream] Cannot drop connection with ID %s: Connection doesn't exist", id)
		} else {
			delete(s.connections, id)
			delete(s.inputs, id)
		}
		s.connectionsLock.Unlock()
		s.inputLock.Unlock()
	}
}

func (s *Server) NewInput([]byte) {
	panic("implement me")
}

func (s *Server) StopServer() {
	s.Core.PauseSignal <- true
}
