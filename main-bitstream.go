//go:build bitstream

package main

import (
	"flag"
	"log"
	"os"

	"github.com/HFO4/gbc-in-cloud/bitstream"
	"github.com/HFO4/gbc-in-cloud/live"
	"gopkg.in/yaml.v3"
)

var (
	h bool

	GUIMode             bool
	FyneMode            bool
	StreamServerMode    bool
	StaticServerMode    bool
	BitmapStreamingMode bool
	LiveMode            bool

	ConfigPath          string
	ListenPort          int
	ROMPath             string
	SoundOn             bool
	FPS                 int
	Debug               bool
	Url                 string
	WebSocketUrl        string
	ConfigFilePath      string
	FullPictureInterval int
)

type Config struct {
	Server struct {
		Live struct {
			Port                int            `yaml:"port"`
			Url                 string         `yaml:"url"`
			FullPictureInterval int            `yaml:"full_picture_interval"`
			Debug               bool           `yaml:"debug"`
			VueVersion          string         `yaml:"vue_version"`
			ICEConfig           live.ICEConfig `yaml:"ice_config"`
		} `yaml:"live,omitempty"`
		Bitstream bitstream.BitstreamServerConfig `yaml:"bitstream,omitempty"`
	} `yaml:"server"`
}

func init() {
	flag.BoolVar(&h, "h", false, "This help")
	flag.BoolVar(&BitmapStreamingMode, "b", false, "Start a WebSocket bitmap streaming server")
	flag.IntVar(&FullPictureInterval, "i", 3, "	Set the non-delta bitmap transmission rate in milliseconds")
	flag.BoolVar(&Debug, "d", false, "Use Debug mode in Server")
	flag.StringVar(&Url, "u", "http://localhost:1989", "Set the server URL")
	flag.StringVar(&WebSocketUrl, "wu", "http://localhost:1989/stream", "Set the WebSocket URL for bitmap streaming server")
	flag.IntVar(&ListenPort, "p", 1989, "Set the server `port`")
	flag.StringVar(&ROMPath, "r", "", "Set `ROM` file path")
	flag.StringVar(&ConfigFilePath, "C", "./config.yaml", "Set path for config YAML file")
}

func runBitstreamServer() {
	f, err := os.Open(ConfigFilePath)
	var config bitstream.BitstreamServerConfig
	if err != nil {
		log.Printf("Config file not found in %s, defaulting to flag values", ConfigFilePath)
		config = bitstream.BitstreamServerConfig{
			Port:                ListenPort,
			GamePath:            ROMPath,
			Url:                 Url,
			FullPictureInterval: FullPictureInterval,
			Debug:               Debug,
		}
	} else {
		defer f.Close()
		var cfg Config
		decoder := yaml.NewDecoder(f)
		err = decoder.Decode(&cfg)
		if err != nil {
			log.Fatalf("Cannot decode YAML config file: %s", err)
		}
		config = cfg.Server.Bitstream
	}
	defer f.Close()
	server := bitstream.BitstreamServer{
		Config: config,
	}
	server.InitServer()
}

func main() {
	flag.Parse()
	if h {
		flag.Usage()
		return
	}

	if BitmapStreamingMode {
		runBitstreamServer()
		return
	}
}
