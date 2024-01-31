package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/HFO4/gbc-in-cloud/live"
	"github.com/HFO4/gbc-in-cloud/static"
	"github.com/HFO4/gbc-in-cloud/stream"
	"gopkg.in/yaml.v3"
)

var (
	h bool

	GUIMode          bool
	FyneMode         bool
	StreamServerMode bool
	StaticServerMode bool
	LiveMode         bool

	ConfigPath string
	ListenPort int
	ROMPath    string
	SoundOn    bool
	FPS        int
	Debug      bool
)

type LiveServerConfig struct {
	Server struct {
		Live struct {
			Port                int            `yaml:"port"`
			Url                 string         `yaml:"url"`
			FullPictureInterval int            `yaml:"full_picture_interval"`
			Debug               bool           `yaml:"debug"`
			VueVersion          string         `yaml:"vue_version"`
			ICEConfig           live.ICEConfig `yaml:"ice_config"`
		} `yaml:"live"`
	} `yaml:"server"`
}

func init() {
	flag.BoolVar(&h, "h", false, "This help")
	flag.BoolVar(&StreamServerMode, "s", false, "Start a cloud-gaming server")
	flag.BoolVar(&StaticServerMode, "S", false, "Start a static image cloud-gaming server")
	flag.BoolVar(&Debug, "d", false, "Use Debugger in GUI mode")
	flag.BoolVar(&LiveMode, "l", false, "Start a WebRTC Live Server")
	flag.IntVar(&ListenPort, "p", 1989, "Set the `port` for the cloud-gaming server")
	flag.StringVar(&ConfigPath, "c", "", "Set the game option list `config` file path")
	flag.StringVar(&ROMPath, "r", "", "Set `ROM` file path")
}

func runStaticServer() {
	server := static.StaticServer{
		Port:     ListenPort,
		GamePath: ROMPath,
	}
	server.Run()
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

	if LiveMode {
		openPath := ""
		if envConfigDir := os.Getenv("GBLIVE_CONFIG_DIR"); envConfigDir != "" {
			openPath = envConfigDir + "/"
		}
		f, err := os.Open(openPath + "config.yaml")
		if err != nil {
			panic(err)
		}
		defer f.Close()
		var cfg LiveServerConfig
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
