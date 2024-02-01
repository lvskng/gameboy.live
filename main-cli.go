package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/HFO4/gbc-in-cloud/bitstream"
	"github.com/HFO4/gbc-in-cloud/live"
	"github.com/HFO4/gbc-in-cloud/static"
	"github.com/HFO4/gbc-in-cloud/stream"
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
	flag.BoolVar(&StreamServerMode, "s", false, "Start a cloud-gaming server")
	flag.BoolVar(&StaticServerMode, "S", false, "Start a static image cloud-gaming server")
	flag.BoolVar(&BitmapStreamingMode, "b", false, "Start a WebSocket bitmap streaming server")
	flag.IntVar(&FullPictureInterval, "i", 3, "	Set the non-delta bitmap transmission rate in milliseconds")
	flag.BoolVar(&Debug, "d", false, "Use Debug mode in Server")
	flag.StringVar(&Url, "u", "http://localhost:1989", "Set the server URL")
	flag.StringVar(&WebSocketUrl, "wu", "http://localhost:1989/stream", "Set the WebSocket URL for bitmap streaming server")
	flag.BoolVar(&LiveMode, "l", false, "Start a WebRTC Live Server")
	flag.IntVar(&ListenPort, "p", 1989, "Set the server `port`")
	flag.StringVar(&ConfigPath, "c", "", "Set the game option list `config` file path")
	flag.StringVar(&ROMPath, "r", "", "Set `ROM` file path")
	flag.StringVar(&ConfigFilePath, "C", "./config.yaml", "Set path for config YAML file")
}

func runStaticServer() {
	server := static.StaticServer{
		Port:     ListenPort,
		GamePath: ROMPath,
	}
	server.Run()
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

func runServer() {
	if ConfigPath == "" {
		log.Fatal("[Error] Game list not specified")
	}

	// Read config file
	configFile, err := os.Open(ConfigPath)
	defer configFile.Close()
	if err != nil {
		log.Fatal("[Error] Failed to read game list config file,", err)
	}
	stats, statsErr := configFile.Stat()
	if statsErr != nil {
		log.Fatal(statsErr)
	}
	var size = stats.Size()
	gameListStr := make([]byte, size)
	bufReader := bufio.NewReader(configFile)
	_, err = bufReader.Read(gameListStr)

	streamServer := new(stream.StreamServer)
	streamServer.Port = ListenPort
	var gameList []stream.GameInfo
	err = json.Unmarshal(gameListStr, &gameList)
	if err != nil {
		log.Fatal("Unable to decode game list config file.")
	}
	streamServer.GameList = gameList
	streamServer.Run()
}

func main() {
	flag.Parse()
	if h {
		flag.Usage()
		return
	}

	if StreamServerMode {
		runServer()
		return
	}

	if StaticServerMode {
		runStaticServer()
		return
	}

	if BitmapStreamingMode {
		runBitstreamServer()
		return
	}

	if LiveMode {
		openPath := ""
		f, err := os.Open(openPath + "config.yaml")
		if err != nil {
			panic(err)
		}
		defer f.Close()
		var cfg Config
		decoder := yaml.NewDecoder(f)
		err = decoder.Decode(&cfg)
		if err != nil {
			panic(err)
		}
		s := &live.LiveServer{Port: cfg.Server.Live.Port, GamePath: ROMPath, Debug: cfg.Server.Live.Debug, FullPictureInterval: cfg.Server.Live.FullPictureInterval, Url: cfg.Server.Live.Url, VueVersion: cfg.Server.Live.VueVersion, ICEConfig: cfg.Server.Live.ICEConfig}
		s.InitServer()
		return
	}
}
