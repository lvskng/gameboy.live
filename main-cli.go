package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/HFO4/gbc-in-cloud/static"
	"github.com/HFO4/gbc-in-cloud/stream"
)

var (
	h bool

	GUIMode          bool
	FyneMode         bool
	StreamServerMode bool
	StaticServerMode bool

	ConfigPath string
	ListenPort int
	ROMPath    string
	SoundOn    bool
	FPS        int
	Debug      bool
)

func init() {
	flag.BoolVar(&h, "h", false, "This help")
	flag.BoolVar(&StreamServerMode, "s", false, "Start a cloud-gaming server")
	flag.BoolVar(&StaticServerMode, "S", false, "Start a static image cloud-gaming server")
	flag.BoolVar(&Debug, "d", false, "Use Debugger in GUI mode")
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
}
