package live

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/HFO4/gbc-in-cloud/gb"
	"github.com/google/uuid"
	"github.com/pion/webrtc/v3"
)

type LiveServer struct {
	Port     int
	GamePath string

	pixels    *[160][144]uint8
	pixelLock sync.RWMutex

	inputStatus *byte
	lastInput   byte
	inputs      map[string]byte

	drawSignal chan bool

	connections map[string]*webrtc.PeerConnection
	channels    map[string]*webrtc.DataChannel

	Core *gb.Core

	Url                 string
	FullPictureInterval int
	Debug               bool
	VueVersion          string
	ICEConfig           ICEConfig
}

type pixelCluster struct {
	pixel    byte
	repindex uint8
	repcount uint8
}

type WebRTCOffer struct {
	Session webrtc.SessionDescription `json:"session"`
	Id      string                    `json:"id"`
}

var candidateData struct {
	ID        string                  `json:"id"`
	Candidate webrtc.ICECandidateInit `json:"candidate"`
}

type PageData struct {
	ServerURL  string
	GameName   string
	DebugMin   string
	VueVersion string
	ClientID   string
	ICEServers []ClientICEServer `json:"iceServers"`
}

type ClientICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type ICEConfig struct {
	Server []struct {
		URLs               []string                 `yaml:"urls"`
		Username           string                   `yaml:"username,omitempty"`
		Credential         interface{}              `yaml:"credential,omitempty"`
		CredentialType     webrtc.ICECredentialType `yaml:"credential_type,omitempty"`
		DynamicCredentials bool                     `yaml:"dynamic_credentials,omitempty"`
	} `yaml:"server"`
	Client []struct {
		URLs               []string `yaml:"urls"`
		Username           string   `yaml:"username,omitempty"`
		Credential         string   `yaml:"credential,omitempty"`
		DynamicCredentials bool     `yaml:"dynamic_credentials,omitempty"`
	} `yaml:"client"`
	DynamicCredentialSecret string `yaml:"dynamic_credential_secret,omitempty"`
	DynamicCredentialTTL    int    `yaml:"dynamic_credential_ttl,omitempty"`
}

// Run Running the static-image gaming server

func (d *LiveServer) Run(sig chan bool, f func()) {
	panic("implement me")
}

func (s *LiveServer) InitServer() {
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
	}
	s.Core = core
	s.drawSignal = core.DrawSignal
	core.Init(s.GamePath)
	go core.Run()

	s.connections = make(map[string]*webrtc.PeerConnection)
	s.channels = make(map[string]*webrtc.DataChannel)
	s.inputs = make(map[string]uint8)
	s.lastInput = 0xFF

	//Implement WebRTC handling
	servers := []webrtc.ICEServer{}
	for _, srv := range s.ICEConfig.Server {
		if srv.DynamicCredentials {
			ttl := int64(s.ICEConfig.DynamicCredentialTTL)
			timestamp := time.Now().Unix() + ttl
			username := fmt.Sprintf("%d:%s", timestamp, "server")
			hmac := hmac.New(sha1.New, []byte(s.ICEConfig.DynamicCredentialSecret))
			hmac.Write([]byte(username))
			password := base64.StdEncoding.EncodeToString(hmac.Sum(nil))
			servers = append(servers, webrtc.ICEServer{URLs: srv.URLs, Username: username, Credential: password})
		} else if srv.Username != "" && srv.Credential != "" {
			servers = append(servers, webrtc.ICEServer{URLs: srv.URLs, Username: srv.Username, Credential: srv.Credential})
		} else {
			servers = append(servers, webrtc.ICEServer{URLs: srv.URLs})
		}
	}
	config := webrtc.Configuration{
		ICEServers: servers,
	}

	http.HandleFunc("/offer", func(w http.ResponseWriter, r *http.Request) {
		// Decode the incoming offer
		var offer webrtc.SessionDescription
		err := json.NewDecoder(r.Body).Decode(&offer)
		if err != nil {
			http.Error(w, "Invalid offer: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Handle the offer and create an answer
		peerConnection, err := webrtc.NewPeerConnection(config)
		if err != nil {
			log.Printf("[Live] Error creating peer connection: %v", err)
			http.Error(w, "Error creating peer connection", http.StatusBadRequest)
			return
		}
		queryParams := r.URL.Query()
		id := queryParams.Get("id")
		if s.connections[id] != nil || s.channels[id] != nil {
			log.Printf("[Live] Error handling offer: Client ID is alrready occupied")
			http.Error(w, "Client ID is already occupied", http.StatusForbidden)
			return
		}
		s.connections[id] = peerConnection
		ans, err := s.handleWebRTCOffer(peerConnection, offer)
		if err != nil {
			log.Printf("[Live] Error creating server: %s", err)
			return
		}
		answer := &WebRTCOffer{Id: id, Session: ans}
		dataChannel, err := peerConnection.CreateDataChannel("data", nil)
		s.channels[id] = dataChannel
		if err != nil {
			log.Printf("[Live] Error creating data channel: %s", err)
			return
		}
		peerConnection.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
			fmt.Printf("Peer Connection State has changed: %s\n", st.String())
			if st == webrtc.PeerConnectionStateFailed {
				// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
				// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
				// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
				log.Printf("Peer Connection has gone to failed: %s", st.String())
				s.DropConnection(id)
			} else if st == webrtc.PeerConnectionStateDisconnected {
				log.Printf("Peer disconnected: %s", st.String())
				s.DropConnection(id)
			}
		})
		dataChannel.OnOpen(func() {
			//Send initial screen
			bitmap, _ := s.GetBitmap()
			msg := []byte{0xFA}
			for _, line := range bitmap {
				msg = append(msg, line...)
			}
			dataChannel.Send(msg)
		})
		dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
			s.handleInput(msg, id)
		})
		if err != nil {
			http.Error(w, "Error handling offer: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(answer); err != nil {
			http.Error(w, "Error encoding answer: "+err.Error(), http.StatusInternalServerError)
		}
	})

	fs := http.FileServer(http.Dir("client"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check if the request is for index.html
		if strings.ToLower(r.URL.Path) == "/" || strings.ToLower(r.URL.Path) == "/index.html" {
			// Parse the HTML template
			tmpl, err := template.ParseFiles("client/index.html")
			if err != nil {
				log.Fatal("Error parsing template:", err)
				return
			}
			debugmin := "min."
			if s.Debug {
				debugmin = ""
			}

			id := uuid.New().String()

			// Create page data with the server URL
			data := PageData{
				ServerURL:  s.Url,
				GameName:   core.GameTitle,
				DebugMin:   debugmin,
				VueVersion: s.VueVersion,
				ICEServers: s.getClientICEConfig(id),
				ClientID:   id,
			}
			w.Header().Add("Access-Control-Allow-Origin", s.Url)
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
	http.HandleFunc("/candidate", func(w http.ResponseWriter, r *http.Request) {
		err := json.NewDecoder(r.Body).Decode(&candidateData)
		if err != nil {
			http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		if peerConnection, ok := s.connections[candidateData.ID]; ok {
			err = peerConnection.AddICECandidate(candidateData.Candidate)
			if err != nil {
				log.Printf("Failed to add ICE Candidate: %s", err)
			}
		} else {
			http.Error(w, "PeerConnection not found", http.StatusNotFound)
		}
	})
	s.serveData()
	http.ListenAndServe(fmt.Sprintf(":%d", s.Port), nil)
	select {}
}
func (server *LiveServer) InitRGB(px *[160][144][3]uint8, s string) {
	// server.pixels = px
	panic("implement me")
}
func (server *LiveServer) Init(px *[160][144]uint8, s string) {
	server.pixels = px
}

func (s *LiveServer) getClientICEConfig(id string) []ClientICEServer {
	ret := []ClientICEServer{}
	for _, srv := range s.ICEConfig.Client {
		if srv.DynamicCredentials {
			ttl := int64(s.ICEConfig.DynamicCredentialTTL)
			timestamp := time.Now().Unix() + ttl
			username := fmt.Sprintf("%d:%s", timestamp, id)
			hmac := hmac.New(sha1.New, []byte(s.ICEConfig.DynamicCredentialSecret))
			hmac.Write([]byte(username))
			password := base64.StdEncoding.EncodeToString(hmac.Sum(nil))
			ret = append(ret, ClientICEServer{
				URLs:       srv.URLs,
				Username:   username,
				Credential: password,
			})
		} else {
			if srv.Username != "" && srv.Credential != "" {
				ret = append(ret, ClientICEServer{
					URLs:       srv.URLs,
					Username:   srv.Username,
					Credential: srv.Credential,
				})
			} else {
				ret = append(ret, ClientICEServer{
					URLs: srv.URLs,
				})
			}
		}
	}
	return ret
}

func (s *LiveServer) serveData() {
	go func() {
		var bitmap [][]byte
		var lastBitmap [160][144]byte
		for {
			select {
			case <-s.drawSignal:
				bitmap, lastBitmap = s.GetBitmapDelta(lastBitmap)
				msg := []byte{0xFB}
				empty := false
				for _, line := range bitmap {
					if line[1] != 0xFE {
						empty = false
					} else {
						empty = true
					}
					msg = append(msg, line...)
				}
				if !empty && len(msg) > 1 {
					for _, c := range s.channels {
						err := c.Send(msg)
						if err != nil {
							log.Printf("[Live] Error sending data: %s", err)
						}
					}
				}
			case <-time.After(time.Duration(s.FullPictureInterval) * time.Second):
				bitmap, lastBitmap = s.GetBitmap()
				msg := []byte{0xFA}
				for _, line := range bitmap {
					msg = append(msg, line...)
				}
				for _, c := range s.channels {
					err := c.Send(msg)
					if err != nil {
						log.Printf("[Live] Error sending data: %s", err)
					}
				}
			}
		}
	}()
}

func (s *LiveServer) handleInput(msg webrtc.DataChannelMessage, id string) {
	input := uint8(msg.Data[0])
	s.inputs[id] = input
}

func (s *LiveServer) InitStatus(b *byte) {
	s.inputStatus = b
	s.lastInput = 0
}

func (s *LiveServer) DropConnection(id string) {
	delete(s.connections, id)
	delete(s.channels, id)
	delete(s.inputs, id)
}

func (s *LiveServer) UpdateInput() bool {
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
	// if winner == s.lastInput {
	// 	return false
	// }
	*s.inputStatus = winner
	return true
}

func (s *LiveServer) NewInput([]byte) {
	panic("implement me")
}

func (s *LiveServer) handleWebRTCOffer(pc *webrtc.PeerConnection, offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	// Set the remote description to the received SDP offer
	err := pc.SetRemoteDescription(offer)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	// Create an SDP answer
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	// Set the local description to the created SDP answer
	err = pc.SetLocalDescription(answer)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	return *pc.LocalDescription(), nil
}

// validates a delta screen for debug purposes
func validateDif(difbmp []byte, lastBitmap, orbitmap [160][144]byte) bool {
	shift := func(slc []byte) (byte, []byte) {
		if len(slc) == 1 {
			return slc[0], []byte{}
		}
		return slc[0], slc[1:]
	}
	data := difbmp
	var b byte
	if b, data = shift(data); b != 0xFB {
		return false
	}
	x := 0
	y := 0
	for len(data) > 0 {
		var op byte
		op, data = shift(data)
		if op > 0x03 && op < 0xA5 {
			x = int(op - 4)
			y = 0
		} else if op == 0xF0 {
			for data[0] < 0x04 || data[0] == 0xFF {
				var pixel byte
				pixel, data = shift(data)
				if pixel != 0xFF {
					lastBitmap[x][y] = pixel
				}
			}
		} else if op == 0xF1 {
			for len(data) > 0 && (data[0] < 0x04 || data[0] > 0xA4) {
				b, data = shift(data)
				if b < 0x04 {
					lastBitmap[x][y] = b
					y++
				} else if b == 0xFF {
					y++
				} else if b == 0xF2 {
					var rcount byte
					var rpx byte
					rcount, data = shift(data)
					rpx, data = shift(data)
					for i := 0; i <= int(rcount); i++ {
						if rpx != 0xFF {
							lastBitmap[x][y] = rpx
						}
						y++
					}
				} else if b > 0xC0 && b < 0xD0 {
					var rpx byte
					rcount := b - 0xC0
					rpx, data = shift(data)
					for i := 0; i <= int(rcount); i++ {
						if rpx != 0xFF {
							lastBitmap[x][y] = rpx
						}
						y++
					}
				} else if b == 0xFD {
					var rpx byte
					rpx, data = shift(data)
					for ; y < 143; y++ {
						if rpx != 0xFF {
							lastBitmap[x][y] = rpx
						}
					}
				}
			}
		} else if op == 0xFE {
			continue
		}
	}
	var pixels [][4]byte
	var lines []int
	for i, line := range lastBitmap {
		if line != orbitmap[i] {
			for index, pixel := range line {
				if pixel != orbitmap[i][index] { //line, pos, expected, is
					pixels = append(pixels, [4]byte{byte(i), byte(index), orbitmap[i][index], pixel})
				}
			}
			lines = append(lines, i)
		}
	}
	var difstrings [][]byte
	for _, linenum := range lines {
		record := false
		var lastb byte
		var ds []byte
		for _, b := range difbmp {
			if b == byte(linenum+0x04) {
				record = true
			} else if (b > 0x03 && b < 0xA5) && (lastb != 0xF2) {
				record = false
			}
			if record {
				ds = append(ds, b)
				lastb = b
			}
		}
		if len(ds) > 0 {
			difstrings = append(difstrings, ds)
		}
	}
	// for _, d := range difstrings {
	// 	log.Printf("difstrings for invalid lines: %s", d)
	// }
	return len(pixels) == 0
}

// Get a compressed bitmap of the current screen
func (s *LiveServer) GetBitmap() ([][]byte, [160][144]byte) {
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

// Get a compressed bitmap of the current screen as delta (difference to last screen)
func (s *LiveServer) GetBitmapDelta(lastBitmap [160][144]byte) ([][]byte, [160][144]byte) {
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

// // Render raw pixels into images
// func (s *LiveServer) EnqueueInput(button byte) {
// 	s.queueLock.Lock()
// 	s.inputQueue = append(s.inputQueue, &inputCommand{button, 3, false})
// 	s.queueLock.Unlock()
// }

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

// func XcompressLine(line []byte, clusters []pixelCluster) []byte {
// 	compressedLine := []byte{}
// 	if len(clusters) > 0 {
// 		compressedLine = append(compressedLine, 0xF1)
// 		var cluster pixelCluster
// 		cluster = clusters[0]
// 		clusters = clusters[1:]
// 		nextindex := -1
// 		for index, pixel := range line {
// 			if nextindex > 0 && index < nextindex {
// 				continue
// 			} else if cluster.repindex > uint8(index) {
// 				compressedLine = append(compressedLine, pixel)
// 				nextindex = -1
// 			} else {
// 				if (len(line)-1)-index > int(cluster.repcount) {
// 					if cluster.repcount < 16 {
// 						compressedLine = append(compressedLine, 0xC0+cluster.repcount)
// 						compressedLine = append(compressedLine, cluster.pixel)
// 					} else {
// 						compressedLine = append(compressedLine, 0xF2)
// 						compressedLine = append(compressedLine, cluster.repcount)
// 						compressedLine = append(compressedLine, cluster.pixel)
// 					}
// 				} else {
// 					compressedLine = append(compressedLine, 0xFD)
// 					compressedLine = append(compressedLine, cluster.pixel)
// 					break
// 				}
// 				nextindex = index + int(cluster.repcount) + 1
// 				if len(clusters) > 0 {
// 					cluster = clusters[0]
// 					clusters = clusters[1:]
// 				} else {
// 					break
// 				}
// 			}
// 		}
// 	} else {
// 		compressedLine = append(compressedLine, 0xF0)
// 		compressedLine = append(compressedLine, line...)
// 	}
// 	return compressedLine
// }
