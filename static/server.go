package static

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/HFO4/gbc-in-cloud/driver"
	"github.com/HFO4/gbc-in-cloud/gb"
	"github.com/gorilla/websocket"
)

type StaticServer struct {
	Port     int
	GamePath string

	driver   *driver.StaticImage
	upgrader websocket.Upgrader
}

type compressedBitmapStreamMessage struct {
	Type string
	Data [][]byte
}

// Run Running the static-image gaming server
func (server *StaticServer) Run() {
	// startup the emulator
	server.driver = &driver.StaticImage{}
	server.upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		return true
	}}
	core := &gb.Core{
		FPS:           60,
		Clock:         4194304,
		Debug:         false,
		DisplayDriver: server.driver,
		Controller:    server.driver,
		DrawSignal:    make(chan bool),
		SpeedMultiple: 0,
		ToggleSound:   false,
	}
	go core.DisplayDriver.Run(core.DrawSignal, func() {})
	core.Init(server.GamePath)
	go core.Run()

	// image and control server
	http.HandleFunc("/image", showImage(server))
	http.HandleFunc("/stream", streamImages(server))
	http.HandleFunc("/cstream", streamCompressedDifImages(server))
	http.HandleFunc("/svg", showSVG(server))
	http.HandleFunc("/control", newInput(server))
	http.ListenAndServe(fmt.Sprintf(":%d", server.Port), nil)
}

func streamCompressedDifImages(server *StaticServer) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		c, err := server.upgrader.Upgrade(w, req, nil)
		if err != nil {
			log.Print(":upgrade error: ", err)
			return
		}
		defer c.Close()
		tick := 0
		go func() {
			for {
				_, msg, err2 := c.ReadMessage()
				stringMsg := string(msg)
				if err2 != nil {
					log.Println(err2)
					break
				}
				buttonByte, err3 := strconv.ParseUint(stringMsg, 10, 32)
				if err3 != nil {
					log.Println(err3)
					continue
				}
				if buttonByte > 7 {
					log.Printf("Received input (%s) > 7", stringMsg)
					continue
				}
				server.driver.EnqueueInput(byte(buttonByte))
			}
		}()
		var bitmap [][]byte
		var lastBitmap [160][144]byte
		for {
			var connType byte
			if tick%2000 == 0 {
				bitmap, lastBitmap = server.driver.GetBitmap()
				connType = 0xFA
			} else {
				bitmap, lastBitmap = server.driver.GetBitmapDelta(lastBitmap)
				connType = 0xFB
			}
			//Debug
			// img := server.driver.Render()
			// var imageBuf bytes.Buffer
			// png.Encode(&imageBuf, img)
			// if snapshot, err := os.Create("snapshots/curr.png"); err == nil {
			// 	png.Encode(snapshot, img)
			// 	snapshot.Close()
			// }
			msg := []byte{connType}
			empty := false
			for _, line := range bitmap {
				if line[1] != 0xFE {
					empty = false
				} else {
					empty = true
				}
				msg = append(msg, line...)
			}
			// _, orbitmap := server.driver.GetBitmap()
			// if !validateDif(msg, lastBitmap, orbitmap) {
			// 	log.Printf("cannot validate dif image")
			// }
			if (connType == 0xFA || !empty) && len(msg) > 1 {
				err = c.WriteMessage(websocket.BinaryMessage, msg)
			}
			if err != nil {
				log.Println("write error:", err)
				break
			}
			// jsonobj := &compressedBitmapStreamMessage{Type: connType, Data: bitmap}
			// if err != nil {
			// 	log.Printf("error marshalling message: %s", err)
			// }
			// err = c.WriteJSON(jsonobj)
			// if err != nil {
			// 	log.Println("write error:", err)
			// 	break
			// }
			tick++
			//time.Sleep(16 * time.Millisecond)
		}

	}
}

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

func streamImages(server *StaticServer) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		c, err := server.upgrader.Upgrade(w, req, nil)
		if err != nil {
			log.Print(":upgrade error: ", err)
			return
		}
		defer c.Close()
		go func() {
			for {
				_, msg, err2 := c.ReadMessage()
				stringMsg := string(msg)
				if err2 != nil {
					log.Println(err2)
					break
				}
				buttonByte, err3 := strconv.ParseUint(stringMsg, 10, 32)
				if err3 != nil {
					log.Println(err3)
					continue
				}
				if buttonByte > 7 {
					log.Printf("Received input (%s) > 7", stringMsg)
					continue
				}
				server.driver.EnqueueInput(byte(buttonByte))
			}
		}()
		for {
			img := server.driver.Render()
			buf := new(bytes.Buffer)
			err = png.Encode(buf, img)
			if err != nil {
				log.Println(err)
				continue
			}
			err = c.WriteMessage(websocket.BinaryMessage, buf.Bytes())
			if err != nil {
				log.Println("write error:", err)
				break
			}
		}
	}
}

func showSVG(server *StaticServer) func(http.ResponseWriter, *http.Request) {
	svg, _ := ioutil.ReadFile("gb.svg")

	return func(w http.ResponseWriter, req *http.Request) {
		callback, _ := req.URL.Query()["callback"]

		w.Header().Set("Cache-control", "no-cache,max-age=0")
		w.Header().Set("Content-type", "image/svg+xml")
		w.Header().Set("Expires", time.Now().Add(time.Duration(-1)*time.Hour).UTC().Format(http.TimeFormat))

		// Encode image to Base64
		img := server.driver.Render()
		var imageBuf bytes.Buffer
		png.Encode(&imageBuf, img)
		encoded := base64.StdEncoding.EncodeToString(imageBuf.Bytes())

		// Embaded image into svg template
		res := strings.ReplaceAll(string(svg), "{image}", "data:image/png;base64,"+encoded)

		// Replace callback url
		res = strings.ReplaceAll(res, "{callback}", callback[0])

		w.Write([]byte(res))
	}
}

func showImage(server *StaticServer) func(http.ResponseWriter, *http.Request) {
	lastSave := time.Now().Add(time.Duration(-1) * time.Hour)
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-control", "no-cache,max-age=0")
		w.Header().Set("Content-type", "image/png")
		w.Header().Set("Expires", time.Now().Add(time.Duration(-1)*time.Hour).UTC().Format(http.TimeFormat))
		img := server.driver.Render()
		png.Encode(w, img)

		// Save snapshot every 10 minutes
		if time.Now().Sub(lastSave).Minutes() > 10 {
			lastSave = time.Now()
			if snapshot, err := os.Create("snapshots/" + strconv.FormatInt(time.Now().Unix(), 10) + ".png"); err == nil {
				png.Encode(snapshot, img)
				snapshot.Close()
			} else {
				fmt.Println(err)
			}
		}
	}
}

func newInput(server *StaticServer) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		keys, ok := req.URL.Query()["button"]
		callback, _ := req.URL.Query()["callback"]

		if !ok || len(keys) < 1 || len(callback) < 1 {
			return
		}

		buttonByte, err := strconv.ParseUint(keys[0], 10, 32)
		if err != nil || buttonByte > 7 {
			return
		}

		server.driver.EnqueueInput(byte(buttonByte))
		time.Sleep(time.Duration(500) * time.Millisecond)
		http.Redirect(w, req, callback[0], http.StatusSeeOther)
	}
}
