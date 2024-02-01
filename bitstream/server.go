package bitstream

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/HFO4/gbc-in-cloud/gb"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type BitstreamServer struct {
	Core *gb.Core

	pixels    *[160][144][3]uint8
	pixelLock sync.RWMutex

	drawSignal chan bool

	connections map[string]struct {
		c *websocket.Conn
		l *sync.Mutex
	}
	inputs      map[string]byte
	inputLock   sync.Mutex
	inputStatus *byte
	lastInput   byte

	Config BitstreamServerConfig

	Debug bool

	upgrader websocket.Upgrader

	inputChannel chan struct {
		id  string
		msg []byte
	}
	dropConnectionChannel chan string
}

type BitstreamServerConfig struct {
	Port                int    `yaml:"port"`
	GamePath            string `yaml:"game_path"`
	WebSocketUrl        string `yaml:"websocket_url"`
	Url                 string `yaml:"url"`
	FullPictureInterval int    `yaml:"full_picture_interval,omitempty"`
	Debug               bool   `yaml:"debug,omitempty"`
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

func (s *BitstreamServer) Run(sig chan bool, f func()) {
	panic("implement me")
}

// Run Running the static-image gaming server
func (s *BitstreamServer) InitServer() {
	// startup the emulator
	// server.upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
	// 	return true
	// }}
	s.upgrader = websocket.Upgrader{}
	core := &gb.Core{
		FPS:           60,
		Clock:         4194304,
		Debug:         false,
		DisplayDriver: s,
		Controller:    s,
		DrawSignal:    make(chan bool),
		SpeedMultiple: 0,
		ToggleSound:   false,
	}
	s.Core = core
	s.drawSignal = core.DrawSignal
	core.Init(s.Config.GamePath)
	go core.Run()

	s.connections = make(map[string]struct {
		c *websocket.Conn
		l *sync.Mutex
	})
	s.inputs = make(map[string]byte)
	s.inputChannel = make(chan struct {
		id  string
		msg []byte
	})
	s.lastInput = 0xFF
	http.HandleFunc("/stream", initStream(s))
	fs := http.FileServer(http.Dir("ws-client"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))
	http.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf(("wassup"))
	})
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

func (s *BitstreamServer) Init(px *[160][144][3]uint8, str string) {
	s.pixels = px
}

func initStream(s *BitstreamServer) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		c, err := s.upgrader.Upgrade(w, req, nil)
		if err != nil {
			log.Printf("[Static] Upgrde error: %v", err)
		}

		id := uuid.New().String()
		conn := struct {
			c *websocket.Conn
			l *sync.Mutex
		}{c: c, l: &sync.Mutex{}}
		s.connections[id] = conn

		s.inputs[id] = 0xFF //All buttons released

		go func() {
			for {
				select {
				case dropId := <-s.dropConnectionChannel:
					if dropId == id {
						s.DropConnection(id)
						return
					}
				default:
					_, msg, err := c.ReadMessage()
					if err != nil {
						log.Printf("[Static] Error reading from channel %s: %v", id, err)
						s.DropConnection(id)
						break
					}
					if len(msg) > 0 {
						s.inputChannel <- struct {
							id  string
							msg []byte
						}{id: id, msg: msg}
					}
				}
			}
		}()
		bitmap, _ := s.GetBitmap()
		msg := []byte{0xFA}
		for _, line := range bitmap {
			msg = append(msg, line...)
		}
		s.SendMessage(conn, msg, id)
	}
}

func (s *BitstreamServer) GetBitmap() ([][]byte, [160][144]byte) {
	s.pixelLock.RLock()
	screen := tidyPixels(*s.pixels)
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
	screen := tidyPixels(*s.pixels)
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

// reduce pixel colors to one numerical value
func tidyPixels(pixels [160][144][3]byte) [160][144]byte {
	var screen [160][144]byte
	for y := 0; y < 144; y++ {
		for x := 0; x < 160; x++ {
			r, g, b := pixels[x][y][0], pixels[x][y][1], pixels[x][y][2]
			var color byte

			if r == 0xFF && g == 0xFF && b == 0xFF {
				color = 0x00
			} else if r == 0xCC && g == 0xCC && b == 0xCC {
				color = 0x01
			} else if r == 0x77 && g == 0x77 && b == 0x77 {
				color = 0x02
			} else {
				color = 0x03
			}

			screen[x][y] = color
		}
	}
	return screen
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
	for {
		select {
		case <-s.drawSignal:
			if sendFullImage {
				sendFullImage = false
				bitmap, lastBitmap = s.GetBitmap()
				msg := []byte{0xFA}
				for _, line := range bitmap {
					msg = append(msg, line...)
				}
				go func() {
					for id, c := range s.connections {
						s.SendMessage(c, msg, id)
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
						for id, c := range s.connections {
							s.SendMessage(c, msg, id)
						}
					}()
				}
			}
		case <-ticker.C:
			sendFullImage = true
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

func (s *BitstreamServer) SendMessage(conn struct {
	c *websocket.Conn
	l *sync.Mutex
}, msg []byte, id string) {
	conn.l.Lock()
	defer conn.l.Unlock()
	err := conn.c.WriteMessage(websocket.BinaryMessage, msg)
	if err != nil {
		log.Printf("[Static] Error sending data: %s", err)
		s.DropConnection(id)
	}
}

func (s *BitstreamServer) DropConnection(id string) {
	log.Printf("[Static] Dropping connection with ID: %s", id)
	conn := s.connections[id]
	conn.l.Lock()
	conn.c.Close()
	delete(s.connections, id)
	delete(s.inputs, id)
	s.dropConnectionChannel <- id
}

func (s *BitstreamServer) NewInput([]byte) {
	panic("implement me")
}

// func streamCompressedDifImages(server *BitstreamServer) func(http.ResponseWriter, *http.Request) {
// 	return func(w http.ResponseWriter, req *http.Request) {
// 		c, err := server.upgrader.Upgrade(w, req, nil)
// 		if err != nil {
// 			log.Print(":upgrade error: ", err)
// 			return
// 		}
// 		defer c.Close()
// 		tick := 0
// 		go func() {
// 			for {
// 				_, msg, err2 := c.ReadMessage()
// 				stringMsg := string(msg)
// 				if err2 != nil {
// 					log.Println(err2)
// 					break
// 				}
// 				buttonByte, err3 := strconv.ParseUint(stringMsg, 10, 32)
// 				if err3 != nil {
// 					log.Println(err3)
// 					continue
// 				}
// 				if buttonByte > 7 {
// 					log.Printf("Received input (%s) > 7", stringMsg)
// 					continue
// 				}
// 				server.driver.EnqueueInput(byte(buttonByte))
// 			}
// 		}()
// 		var bitmap [][]byte
// 		var lastBitmap [160][144]byte
// 		for {
// 			var connType byte
// 			if tick%1000 == 0 {
// 				bitmap, lastBitmap = server.driver.GetBitmap()
// 				connType = 0xFA
// 			} else {
// 				bitmap, lastBitmap = server.driver.GetBitmapDelta(lastBitmap)
// 				connType = 0xFB
// 			}
// 			//Debug
// 			// img := server.driver.Render()
// 			// var imageBuf bytes.Buffer
// 			// png.Encode(&imageBuf, img)
// 			// if snapshot, err := os.Create("snapshots/curr.png"); err == nil {
// 			// 	png.Encode(snapshot, img)
// 			// 	snapshot.Close()
// 			// }
// 			msg := []byte{connType}
// 			empty := false
// 			for _, line := range bitmap {
// 				if line[1] != 0xFE {
// 					empty = false
// 				} else {
// 					empty = true
// 				}
// 				msg = append(msg, line...)
// 			}
// 			//validate screen for debug purposes
// 			// _, orbitmap := server.driver.GetBitmap()
// 			// if !validateDif(msg, lastBitmap, orbitmap) {
// 			// 	log.Printf("cannot validate dif image")
// 			// }
// 			if (connType == 0xFA || !empty) && len(msg) > 1 {
// 				err = c.WriteMessage(websocket.BinaryMessage, msg)
// 			}
// 			if err != nil {
// 				log.Println("write error:", err)
// 				break
// 			}
// 			tick++
// 			//time.Sleep(16 * time.Millisecond)
// 		}

// 	}
// }

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

// func streamImages(server *BitstreamServer) func(http.ResponseWriter, *http.Request) {
// 	return func(w http.ResponseWriter, req *http.Request) {
// 		c, err := server.upgrader.Upgrade(w, req, nil)
// 		if err != nil {
// 			log.Print(":upgrade error: ", err)
// 			return
// 		}
// 		defer c.Close()
// 		go func() {
// 			for {
// 				_, msg, err2 := c.ReadMessage()
// 				stringMsg := string(msg)
// 				if err2 != nil {
// 					log.Println(err2)
// 					break
// 				}
// 				buttonByte, err3 := strconv.ParseUint(stringMsg, 10, 32)
// 				if err3 != nil {
// 					log.Println(err3)
// 					continue
// 				}
// 				if buttonByte > 7 {
// 					log.Printf("Received input (%s) > 7", stringMsg)
// 					continue
// 				}
// 				server.driver.EnqueueInput(byte(buttonByte))
// 			}
// 		}()
// 		for {
// 			img := server.driver.Render()
// 			buf := new(bytes.Buffer)
// 			err = png.Encode(buf, img)
// 			if err != nil {
// 				log.Println(err)
// 				continue
// 			}
// 			err = c.WriteMessage(websocket.BinaryMessage, buf.Bytes())
// 			if err != nil {
// 				log.Println("write error:", err)
// 				break
// 			}
// 		}
// 	}
// }

// func showSVG(server *BitstreamServer) func(http.ResponseWriter, *http.Request) {
// 	svg, _ := ioutil.ReadFile("gb.svg")

// 	return func(w http.ResponseWriter, req *http.Request) {
// 		callback, _ := req.URL.Query()["callback"]

// 		w.Header().Set("Cache-control", "no-cache,max-age=0")
// 		w.Header().Set("Content-type", "image/svg+xml")
// 		w.Header().Set("Expires", time.Now().Add(time.Duration(-1)*time.Hour).UTC().Format(http.TimeFormat))

// 		// Encode image to Base64
// 		img := server.driver.Render()
// 		var imageBuf bytes.Buffer
// 		png.Encode(&imageBuf, img)
// 		encoded := base64.StdEncoding.EncodeToString(imageBuf.Bytes())

// 		// Embaded image into svg template
// 		res := strings.ReplaceAll(string(svg), "{image}", "data:image/png;base64,"+encoded)

// 		// Replace callback url
// 		res = strings.ReplaceAll(res, "{callback}", callback[0])

// 		w.Write([]byte(res))
// 	}
// }

// func showImage(server *BitstreamServer) func(http.ResponseWriter, *http.Request) {
// 	lastSave := time.Now().Add(time.Duration(-1) * time.Hour)
// 	return func(w http.ResponseWriter, req *http.Request) {
// 		w.Header().Set("Cache-control", "no-cache,max-age=0")
// 		w.Header().Set("Content-type", "image/png")
// 		w.Header().Set("Expires", time.Now().Add(time.Duration(-1)*time.Hour).UTC().Format(http.TimeFormat))
// 		img := server.driver.Render()
// 		png.Encode(w, img)

// 		// Save snapshot every 10 minutes
// 		if time.Now().Sub(lastSave).Minutes() > 10 {
// 			lastSave = time.Now()
// 			if snapshot, err := os.Create("snapshots/" + strconv.FormatInt(time.Now().Unix(), 10) + ".png"); err == nil {
// 				png.Encode(snapshot, img)
// 				snapshot.Close()
// 			} else {
// 				fmt.Println(err)
// 			}
// 		}
// 	}
// }

// func newInput(server *BitstreamServer) func(http.ResponseWriter, *http.Request) {
// 	return func(w http.ResponseWriter, req *http.Request) {
// 		keys, ok := req.URL.Query()["button"]
// 		callback, _ := req.URL.Query()["callback"]

// 		if !ok || len(keys) < 1 || len(callback) < 1 {
// 			return
// 		}

// 		buttonByte, err := strconv.ParseUint(keys[0], 10, 32)
// 		if err != nil || buttonByte > 7 {
// 			return
// 		}

// 		server.driver.EnqueueInput(byte(buttonByte))
// 		time.Sleep(time.Duration(500) * time.Millisecond)
// 		http.Redirect(w, req, callback[0], http.StatusSeeOther)
// 	}
// }
