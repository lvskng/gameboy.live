//go:build wasm
// +build wasm

package main

import (
	"context"
	"github.com/llgcode/draw2d/draw2dimg"
	"github.com/markfarnan/go-canvas/canvas"
	"image"
	"image/color"
	"nhooyr.io/websocket"
	"sync"
	"syscall/js"
)

var screen *image.RGBA
var screenLock sync.RWMutex
var screenUpdated bool
var colors [4][3]byte
var opacity byte

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, _, err := websocket.Dial(ctx, "ws://localhost:1989/stream", nil)
	if err != nil {
		panic("fuck")
	}
	defer c.CloseNow()

	screenLock = sync.RWMutex{}
	screenUpdated = false
	colors = [4][3]byte{
		{0x9b, 0xbc, 0x0f},
		{0x8b, 0xac, 0x0f},
		{0x30, 0x62, 0x30},
		{0x0f, 0x38, 0x0f},
	}
	opacity = 0xFF

	cvs, _ := canvas.NewCanvas2d(false)

	doc := js.Global().Get("document")
	emulatorScreen := doc.Call("getElementById", "emulatorScreen")

	screen = image.NewRGBA(image.Rect(0, 0, 160, 144))

	cvs.Set(emulatorScreen, 160, 144)
	cvs.Start(60, renderScreen)

	go readFromConn(ctx, c)
	select {}
}

func renderScreen(gc *draw2dimg.GraphicContext) bool {
	if screenUpdated {
		//Draw to canvas
		screenLock.RLock()
		defer screenLock.RUnlock()
		gc.DrawImage(screen)
		screenUpdated = false
		return true
	} else {
		return false
	}
}

func drawPixel(pixel byte, xPos, yPos int) {
	screen.Set(xPos, yPos, color.RGBA{R: colors[pixel][0], G: colors[pixel][1], B: colors[pixel][2], A: opacity})
}

func updateScreen(data *[]byte) {
	screenLock.Lock()
	defer screenLock.Unlock()
	screenUpdated = true
	var b byte
	b, data = shift(data) //Get the delta or full header
	x := 0
	y := 0
	for len(*data) > 0 {
		var op byte
		op, data = shift(data)
		if op > 0x03 && op < 0xA5 {
			x = int(op - 4)
			y = 0
		} else if op == 0xF0 {
			for len(*data) > 0 && ((*data)[0] < 0x04 || (*data)[0] == 0xFF) {
				var pixel byte
				pixel, data = shift(data)
				if pixel != 0xFF {
					drawPixel(pixel, x, y)
				}
				y++
			}
		} else if op == 0xF1 {
			for len(*data) > 0 && ((*data)[0] < 0x04 || (*data)[0] > 0xA4) {
				b, data = shift(data)
				if b < 0x04 {
					drawPixel(b, x, y)
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
							drawPixel(rpx, x, y)
						}
						y++
					}
				} else if b > 0xD0 && b < 0xE0 {
					var rpx byte
					rcount := b - 0xD0
					rpx, data = shift(data)
					for i := 0; i <= int(rcount); i++ {
						if rpx != 0xFF {
							drawPixel(rpx, x, y)
						}
						y++
					}
				} else if b == 0xFD {
					var rpx byte
					rpx, data = shift(data)
					for ; y < 144; y++ {
						if rpx != 0xFF {
							drawPixel(rpx, x, y)
						}
					}
				}
			}
		} else if op == 0xFE {
			continue
		}
	}
}

func shift(slc *[]byte) (byte, *[]byte) {
	var r []byte
	if len(*slc) == 1 {
		return (*slc)[0], &r
	}
	r = (*slc)[1:]
	return (*slc)[0], &r
}

func readFromConn(ctx context.Context, c *websocket.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			_, msg, err := c.Read(ctx)
			if err != nil {
				return err
			}
			if len(msg) > 0 {
				//println(fmt.Sprintf("%+v", msg))
				//return nil
				go updateScreen(&msg)
			}
		}

	}
}
