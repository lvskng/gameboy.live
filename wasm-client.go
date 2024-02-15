//go:build wasm
// +build wasm

package main

import (
	"context"
	"github.com/HFO4/gbc-in-cloud/bitstream"
	"github.com/llgcode/draw2d/draw2dimg"
	"image"
	"image/color"
	"nhooyr.io/websocket"
	"sync"
	"syscall/js"
	"unsafe"
)

var screen *image.RGBA
var screenLock sync.RWMutex
var screenUpdated bool
var colors [4][3]byte
var opacity byte

func main() {
	js.Global().Set("getScreen", js.FuncOf(getScreen))
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

	//cvs, _ := canvas.NewCanvas2d(false)

	//doc := js.Global().Get("document")
	//emulatorScreen := doc.Call("getElementById", "emulatorScreen")

	screen = image.NewRGBA(image.Rect(0, 0, 160, 144))

	//cvs.Set(emulatorScreen, 160, 144)
	//cvs.Start(60, renderScreen)

	go readFromConn(ctx, c)
	select {}
}

func getScreen(this js.Value, p []js.Value) interface{} {
	if screen != nil {
		sz := screen.Bounds().Size()
		return []interface{}{uintptr(unsafe.Pointer(&screen.Pix[0])), len(screen.Pix), sz.X, sz.Y}
	}
	return nil
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
	bitstream.DecompressLine(data, drawPixel)
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
