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
var inputChannel chan byte
var exitChannel chan struct{}

// This program is a client for the bitstream WebSocket server
// It is meant to be used together with the client HTML and JS via WebAssembly
func main() {
	ctx, cancel := context.WithCancel(context.Background())
	jsWindow := js.Global()

	//We expect the window.getWSUrl method from the JS to return a string with the WS server
	addr := jsWindow.Call("getWSUrl").String()

	c, _, err := websocket.Dial(ctx, addr, nil)
	if err != nil {
		panic(fmt.Sprintf("WebSocket connection to %s failed: %+v", addr, err))
	}

	defer func() {
		err := c.CloseNow()
		if err != nil {
			println(fmt.Sprintf("Error closing connection: %+v", err))
		}
		cancel()
	}()

	//Initialize fields
	screenLock = sync.RWMutex{}
	screenUpdated = false
	colors = [4][3]byte{
		{0x9b, 0xbc, 0x0f},
		{0x8b, 0xac, 0x0f},
		{0x30, 0x62, 0x30},
		{0x0f, 0x38, 0x0f},
	}
	opacity = 0xFF
	inputChannel = make(chan byte)
	exitChannel = make(chan struct{})

	screen = image.NewRGBA(image.Rect(0, 0, 160, 144))

	//Set the screen update function via requestAnimationScreen
	updateFunc = js.FuncOf(FrameUpdate)
	jsWindow.Call("requestAnimationFrame", updateFunc)

	//Export the Exit, SetColors and UpdateInput functions to JS
	jsWindow.Set("exitClient", js.FuncOf(Exit))
	jsWindow.Set("setColors", js.FuncOf(SetColors))
	jsWindow.Set("updateInput", js.FuncOf(UpdateInput))

	for {
		select {
		case <-exitChannel:
			cancel()
		default:
			err := handleConnection(ctx, c)
			println("WebSocket reading finished")
			//If the handleConnection function returns an error, reattempt connection
			if err != nil {
				println(fmt.Sprintf("WebSocket reading finished with error: %+v", err))
				println("Attempting reconnection")
				c, _, err = websocket.Dial(ctx, addr, nil)
			} else {
				break
			}
		}
	}
}

// FrameUpdate If the screen has been updated since the last rendering event,
// lock the screen and call the JS drawing function with its pointer
func FrameUpdate(this js.Value, args []js.Value) interface{} {
	if screenUpdated && screen != nil {
		screenLock.RLock()
		sz := screen.Bounds().Size()
		//Calls window.drawScreen with a pointer to the screen image data
		//Significantly faster than drawing the canvas in Go
		js.Global().Call("drawScreen", uintptr(unsafe.Pointer(&screen.Pix[0])), len(screen.Pix), sz.X, sz.Y)
		screenLock.RUnlock()
		screenUpdated = false
	}
	js.Global().Call("requestAnimationFrame", updateFunc)
	return nil
}

// Exit the client
func Exit(js.Value, []js.Value) interface{} {
	exitChannel <- struct{}{}
	return nil
}

// UpdateInput Set the input status
// The function expects one byte as an argument, representing the input status
func UpdateInput(this js.Value, p []js.Value) interface{} {
	inputChannel <- byte(p[0].Int())
	return nil
}

// SetColors Set the drawing colors for the DMG screen
// Function asserts 4 parameters: 3 byte arrays of cardinality 3 and one byte
func SetColors(this js.Value, p []js.Value) interface{} {
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

// Draw a pixel to the screen
func drawPixel(pixel byte, xPos, yPos int) {
	screen.Set(xPos, yPos, color.RGBA{R: colors[pixel][0], G: colors[pixel][1], B: colors[pixel][2], A: opacity})
}

// Update the screen with the image data
func updateScreen(data *[]byte) {
	screenLock.Lock()
	defer screenLock.Unlock()
	screenUpdated = true
	bitstream.Decompress(data, drawPixel)
}

// Handles WebSocket connection read and write
func handleConnection(ctx context.Context, c *websocket.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case s := <-inputChannel:
			err := c.Write(ctx, websocket.MessageBinary, []byte{s})
			if err != nil {
				return err
			}
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
