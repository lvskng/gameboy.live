//go:build wasm
// +build wasm

package main

import (
	"context"
	"fmt"
	"github.com/HFO4/gbc-in-cloud/bitstream"
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
var updateFunc js.Func

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := js.Global().Call("getWSUrl").String()

	c, _, err := websocket.Dial(ctx, addr, nil)
	if err != nil {
		panic(fmt.Sprintf("WebSocket connection to %s failed: %+v", addr, err))
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

	screen = image.NewRGBA(image.Rect(0, 0, 160, 144))

	updateFunc = js.FuncOf(frameUpdate)

	js.Global().Call("requestAnimationFrame", updateFunc)

	for {
		err := readFromConn(ctx, c)
		println("WebSocket reading finished")
		if err != nil {
			println(fmt.Sprintf("WebSocket reading finished with error: %+v", err))
			println("Attempting reconnection")
			c, _, err = websocket.Dial(ctx, addr, nil)
		} else {
			break
		}
	}
}

func frameUpdate(this js.Value, args []js.Value) interface{} {
	if screenUpdated && screen != nil {
		sz := screen.Bounds().Size()
		//Length of the Pix slice: 4 (3 colors + opacity) * product of X and Y image length
		pixels := make([]uint8, 4*sz.X*sz.Y)
		screenLock.RLock()
		//Copy screen to new slice for JS "export"
		copy(pixels, screen.Pix)
		screenLock.RUnlock()

		js.Global().Call("drawScreen", uintptr(unsafe.Pointer(&pixels[0])), len(pixels), sz.X, sz.Y)
		screenUpdated = false
	}
	js.Global().Call("requestAnimationFrame", updateFunc)
	return nil
}

// Set the drawing colors for the DMG screen
// Function asserts 4 parameters: 3 arrays of cardinality 3 and one byte
func setColors(this js.Value, p []js.Value) interface{} {
	if len(p) == 4 {
		var newColors [4][3]byte
		for i, c := range p[0:3] {
			col := [3]byte{byte(c.Index(0).Int()), byte(c.Index(1).Int()), byte(c.Index(2).Int())}
			newColors[i] = col
		}
		opacity = byte(p[4].Int())
		colors = newColors
	} else {
		println(fmt.Sprintf("setColors accepts 5 parameters ([3]byte, [3]byte, [3]byte, [3]byte, byte) but %d were given", len(p)))
	}
	return nil
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
				go updateScreen(&msg)
			}
		}

	}
}
