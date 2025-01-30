package bitstream

import (
	"testing"
	"time"
)

var (
	ListenPort          int
	ROMPath             string
	Debug               bool
	Url                 string
	ConfigFilePath      string
	FullPictureInterval int
)

type Config struct {
	Server struct {
		Bitstream BitstreamServerConfig `yaml:"bitstream,omitempty"`
	} `yaml:"server"`
}

func TestServer(t *testing.T) {

	config := BitstreamServerConfig{
		Port:                1989,
		GamePath:            "../rom.gb",
		WebSocketUrl:        "ws://localhost:1989/stream",
		Url:                 "http://localhost:1989",
		FullPictureInterval: 1000,
		Debug:               true,
		ClientWriteTimeout:  5000,
		PauseIfIdle:         true,
		RateLimit: struct {
			ms    int `yaml:"ms"`
			burst int `yaml:"burst"`
		}{100, 8},
	}
	server := Server{
		Config: config,
	}
	go server.InitServer()
	timer := time.NewTimer(5 * time.Minute)
	<-timer.C
}
