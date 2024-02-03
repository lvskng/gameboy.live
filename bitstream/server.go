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
	"github.com/google/uuid"
	"nhooyr.io/websocket"
)

type BitstreamServer struct {
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

type pixelCluster struct {
	pixel    byte
	repindex uint8
	repcount uint8
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
	closed    *bool
}

func (s *BitstreamServer) Run(sig chan bool, f func()) {
	panic("implement me")
}

// Run Running the static-image gaming server
func (s *BitstreamServer) InitServer() {
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
	http.HandleFunc("/stream", s.handleSubscribe)
	fs := http.FileServer(http.Dir("ws-client"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check if the request is for index.html
		if strings.ToLower(r.URL.Path) == "/" || strings.ToLower(r.URL.Path) == "/index.html" {
			// Parse the HTML template
			tmpl, err := template.ParseFiles("ws-client/index.html")
			if err != nil {
				log.Fatal("Error parsing template:", err)
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
				log.Fatal("Error executing template:", err)
			}
		} else {
			// For other files, serve them normally
			fs.ServeHTTP(w, r)
		}
	})
	go s.serveData()
	go s.handleInput()
	http.ListenAndServe(fmt.Sprintf(":%d", s.Config.Port), nil)
	select {}
}

func (s *BitstreamServer) handleSubscribe(w http.ResponseWriter, r *http.Request) {
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

func (s *BitstreamServer) subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request, id string) error {
	var c *websocket.Conn
	mu := sync.Mutex{}
	var closed bool = false
	c2, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}
	mu.Lock()
	if closed {
		mu.Unlock()
		return net.ErrClosed
	}
	c = c2
	conn := &Connection{
		c:      c,
		l:      &mu,
		m:      make(chan []byte, s.SubscriberMessageBuffer),
		closed: &closed,
		closeSlow: func() {
			mu.Lock()
			closed = true
			if c != nil {
				c.Close(websocket.StatusPolicyViolation, "connection too slow")
				mu.Unlock()
				s.DropConnection(id)
			}
		},
	}

	s.addConnection(conn, id)
	defer conn.closeSlow()
	mu.Unlock()
	defer c.CloseNow()

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

func (s *BitstreamServer) addConnection(conn *Connection, id string) {
	s.connectionsLock.Lock()
	s.inputLock.Lock()
	defer s.connectionsLock.Unlock()
	defer s.inputLock.Unlock()
	s.connections[id] = conn
	s.inputs[id] = 0xFF
}

func (s *BitstreamServer) Init(px *[160][144]uint8, str string) {
	s.pixels = px
}

func (s *BitstreamServer) InitRGB(px *[160][144][3]uint8, str string) {
	// s.pixelsRGB = px
	panic("implement me")
}

func writeTimeout(ctx context.Context, timeout time.Duration, conn *Connection, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	closed := *conn.closed
	if closed {
		return errors.New("Connection is closed")
	}
	return conn.c.Write(ctx, websocket.MessageBinary, msg)
}

func (s *BitstreamServer) GetBitmap() ([][]byte, [160][144]byte) {
	s.pixelLock.RLock()
	screen := *s.pixels
	s.pixelLock.RUnlock()
	var retscreen [][]byte
	for linenum, line := range screen {
		lineClusters := getLineClusters(line[:])
		compressedLine := compressLine(line[:], lineClusters)
		retscreen = append(retscreen, append([]byte{byte(linenum) + 0x04}, compressedLine...))
	}
	return retscreen, screen
}

func (s *BitstreamServer) GetBitmapDelta(lastBitmap [160][144]byte) ([][]byte, [160][144]byte) {
	s.pixelLock.RLock()
	screen := *s.pixels
	s.pixelLock.RUnlock()
	var difscreen [][]byte
	for linenum, line := range screen {
		compressedDifline := []byte{byte(linenum) + 0x04}
		var difline []byte
		lastLine := lastBitmap[linenum]

		//replace equal values with 0xFF
		if line == lastLine {
			continue
		}
		for index, pixel := range line {
			if pixel == lastLine[index] {
				difline = append(difline, 0xFF)
			} else {
				difline = append(difline, pixel)
			}
		}
		//clustering
		clusters := getLineClusters(difline)
		compressedDifline = append(compressedDifline, compressLine(difline, clusters)...)
		//line compression validation for debug purposes
		// shift := func(slc []byte) (byte, []byte) {
		// 	if len(slc) == 1 {
		// 		return slc[0], []byte{}
		// 	}
		// 	return slc[0], slc[1:]
		// }
		// data := compressedDifline
		// var b byte
		// if b, data = shift(data); b != 0xFB {
		// 	//nop
		// }
		// y := 0
		// lastline := lastBitmap[linenum]
		// for len(data) > 0 {
		// 	var op byte
		// 	op, data = shift(data)
		// 	if op > 0x03 && op < 0xA5 {
		// 		y = 0
		// 	} else if op == 0xF0 {
		// 		for len(data) > 0 && (data[0] < 0x04 || data[0] == 0xFF) {
		// 			var pixel byte
		// 			pixel, data = shift(data)
		// 			if pixel != 0xFF {
		// 				lastline[y] = pixel
		// 			}
		// 		}
		// 	} else if op == 0xF1 {
		// 		for len(data) > 0 && (data[0] < 0x04 || data[0] > 0xA4) {
		// 			b, data = shift(data)
		// 			if b < 0x04 {
		// 				lastline[y] = b
		// 				y++
		// 			} else if b == 0xFF {
		// 				y++
		// 			} else if b == 0xF2 {
		// 				var rcount byte
		// 				var rpx byte
		// 				rcount, data = shift(data)
		// 				rpx, data = shift(data)
		// 				for i := 0; i <= int(rcount); i++ {
		// 					if rpx != 0xFF {
		// 						lastline[y] = rpx
		// 					}
		// 					y++
		// 				}
		// 			} else if b > 0xC0 && b < 0xD0 {
		// 				var rpx byte
		// 				rcount := b - 0xC0
		// 				rpx, data = shift(data)
		// 				for i := 0; i <= int(rcount); i++ {
		// 					if rpx != 0xFF {
		// 						lastline[y] = rpx
		// 					}
		// 					y++
		// 				}
		// 			} else if b == 0xFD {
		// 				var rpx byte
		// 				rpx, data = shift(data)
		// 				for ; y < 144; y++ {
		// 					if rpx != 0xFF {
		// 						lastline[y] = rpx
		// 					}
		// 				}
		// 			}
		// 		}
		// 	} else if op == 0xFE {
		// 		continue
		// 	}
		// }
		// var pixels [][3]int
		// if lastline != line {
		// 	for n, p := range line {
		// 		if lastline[n] != p { //pos ex act
		// 			pixels = append(pixels, [3]int{n, int(p), int(lastline[n])})
		// 		}
		// 	}
		// }
		difscreen = append(difscreen, compressedDifline)
	}
	return difscreen, screen
}

// get repeating sections of a line for compression
func getLineClusters(line []byte) []pixelCluster {
	clusters := []pixelCluster{}
	var lastpixel byte
	var repcount uint8
	var repindex uint8
	for index, pixel := range line {
		if index == 0 {
			lastpixel = pixel
			continue
		}
		if repcount > 0 {
			if lastpixel == pixel {
				repcount++
				if index == len(line)-1 {
					if repcount > 1 {
						clusters = append(clusters, pixelCluster{lastpixel, repindex, repcount})
					}
				}
			} else {
				if repcount > 1 {
					clusters = append(clusters, pixelCluster{lastpixel, repindex, repcount})
				}
				repcount = 0
				lastpixel = pixel
			}
		} else {
			if lastpixel == pixel {
				repindex = uint8(index - 1) //-1 because the cluster starts at the first equal int
				repcount = 1
			} else {
				lastpixel = pixel
			}
		}
	}
	return clusters
}

func shiftPc(clusters []pixelCluster) (pixelCluster, []pixelCluster) {
	c := clusters[0]
	return c, clusters[1:]
}

// Compresses a display line by eliminating clusters with a repeat declaration
// 0x00 - 0x03 regular pixel color value
// 0xFF no change in pixel value since last image
// 0x04 - 0xA3 line identifier
// 0xCx repeat the following byte x times
// 0xF0 start regular line without compression
// 0xF1 start compressed line
// 0xF2 0xXX repeat the following byte XX times
// 0xFD repeat until end of line
// 0xFE ignore line
// 0xEE internally used to mark array elements that are to be removed
func compressLine(origLine []byte, cl []pixelCluster) []byte {
	line := make([]byte, len(origLine))
	copy(line, origLine)

	var cline []byte
	if len(cl) == 0 {
		return append([]byte{0xF0}, line...)
	}

	cline = make([]byte, 1, len(line)/2)
	cline[0] = 0xF1

	var cluster pixelCluster
	clusters := cl
	hasClusters := len(clusters) > 0
	if !hasClusters {
		return append([]byte{0xF0}, line...)
	}
	for len(clusters) > 0 {
		cluster, clusters = shiftPc(clusters)
		i := cluster.repindex
		clend := i + cluster.repcount
		if clend >= 143 {
			line[i] = 0xFD
			i += 2
		} else if cluster.repcount < 16 {
			line[i] = 0xC0 + cluster.repcount
			i += 2
		} else {
			line[i] = 0xF2
			i++
			line[i] = cluster.repcount
			i += 2
		}
		for ; i <= clend; i++ {
			line[i] = 0xEE //trim
		}
		cline = []byte{0xF1}
	}
	for _, px := range line {
		if px != 0xEE {
			cline = append(cline, px)
		}
	}

	return cline
}

func (s *BitstreamServer) serveData() {
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
							s.rateLimiter.Wait(context.Background())
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
								s.rateLimiter.Wait(context.Background())
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

func (s *BitstreamServer) handleInput() {
	for input := range s.inputChannel {
		s.inputLock.Lock()
		s.inputs[input.id] = input.msg[0]
		s.inputLock.Unlock()
	}
}

func (s *BitstreamServer) InitStatus(b *byte) {
	s.inputStatus = b
	s.lastInput = 0xFF //All buttons released
}

func (s *BitstreamServer) UpdateInput() bool {
	s.inputLock.Lock()
	defer s.inputLock.Unlock()
	if len(s.inputs) == 0 {
		return false
	}
	poll := make(map[byte]int)
	for _, usrStatus := range s.inputs {
		poll[usrStatus]++
	}
	var max int
	var winner byte
	for usrStatus, freq := range poll {
		if freq > max {
			winner = usrStatus
			max = freq
		}
	}
	s.inputs = make(map[string]byte)
	*s.inputStatus = winner
	return true
}

func (s *BitstreamServer) DropConnection(id string) {
	log.Printf("[Bitstream] Dropping connection with ID: %s", id)
	s.connectionsLock.Lock()
	s.inputLock.Lock()
	defer s.connectionsLock.Unlock()
	defer s.inputLock.Unlock()
	delete(s.connections, id)
	delete(s.inputs, id)
}

func (s *BitstreamServer) NewInput([]byte) {
	panic("implement me")
}

// validates a delta screen for debug purposes
// func validateDif(difbmp []byte, lastBitmap, orbitmap [160][144]byte) bool {
// 	shift := func(slc []byte) (byte, []byte) {
// 		if len(slc) == 1 {
// 			return slc[0], []byte{}
// 		}
// 		return slc[0], slc[1:]
// 	}
// 	data := difbmp
// 	var b byte
// 	if b, data = shift(data); b != 0xFB {
// 		return false
// 	}
// 	x := 0
// 	y := 0
// 	for len(data) > 0 {
// 		var op byte
// 		op, data = shift(data)
// 		if op > 0x03 && op < 0xA5 {
// 			x = int(op - 4)
// 			y = 0
// 		} else if op == 0xF0 {
// 			for data[0] < 0x04 || data[0] == 0xFF {
// 				var pixel byte
// 				pixel, data = shift(data)
// 				if pixel != 0xFF {
// 					lastBitmap[x][y] = pixel
// 				}
// 			}
// 		} else if op == 0xF1 {
// 			for len(data) > 0 && (data[0] < 0x04 || data[0] > 0xA4) {
// 				b, data = shift(data)
// 				if b < 0x04 {
// 					lastBitmap[x][y] = b
// 					y++
// 				} else if b == 0xFF {
// 					y++
// 				} else if b == 0xF2 {
// 					var rcount byte
// 					var rpx byte
// 					rcount, data = shift(data)
// 					rpx, data = shift(data)
// 					for i := 0; i <= int(rcount); i++ {
// 						if rpx != 0xFF {
// 							lastBitmap[x][y] = rpx
// 						}
// 						y++
// 					}
// 				} else if b > 0xC0 && b < 0xD0 {
// 					var rpx byte
// 					rcount := b - 0xC0
// 					rpx, data = shift(data)
// 					for i := 0; i <= int(rcount); i++ {
// 						if rpx != 0xFF {
// 							lastBitmap[x][y] = rpx
// 						}
// 						y++
// 					}
// 				} else if b == 0xFD {
// 					var rpx byte
// 					rpx, data = shift(data)
// 					for ; y < 143; y++ {
// 						if rpx != 0xFF {
// 							lastBitmap[x][y] = rpx
// 						}
// 					}
// 				}
// 			}
// 		} else if op == 0xFE {
// 			continue
// 		}
// 	}
// 	var pixels [][4]byte
// 	var lines []int
// 	for i, line := range lastBitmap {
// 		if line != orbitmap[i] {
// 			for index, pixel := range line {
// 				if pixel != orbitmap[i][index] { //line, pos, expected, is
// 					pixels = append(pixels, [4]byte{byte(i), byte(index), orbitmap[i][index], pixel})
// 				}
// 			}
// 			lines = append(lines, i)
// 		}
// 	}
// 	var difstrings [][]byte
// 	for _, linenum := range lines {
// 		record := false
// 		var lastb byte
// 		var ds []byte
// 		for _, b := range difbmp {
// 			if b == byte(linenum+0x04) {
// 				record = true
// 			} else if (b > 0x03 && b < 0xA5) && (lastb != 0xF2) {
// 				record = false
// 			}
// 			if record {
// 				ds = append(ds, b)
// 				lastb = b
// 			}
// 		}
// 		if len(ds) > 0 {
// 			difstrings = append(difstrings, ds)
// 		}
// 	}
// 	// for _, d := range difstrings {
// 	// 	log.Printf("difstrings for invalid lines: %s", d)
// 	// }
// 	return len(pixels) == 0
// }
