package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"gopkg.in/alecthomas/kingpin.v2"
	"gopkg.in/yaml.v2"
)

var Version string = "v0.0.0"

type trackFlow int

const (
	_TRACK_FLOW_RTP trackFlow = iota
	_TRACK_FLOW_RTCP
)

type track struct {
	rtpPort  int
	rtcpPort int
}

type streamProtocol int

const (
	_STREAM_PROTOCOL_UDP streamProtocol = iota
	_STREAM_PROTOCOL_TCP
)

func (s streamProtocol) String() string {
	if s == _STREAM_PROTOCOL_UDP {
		return "udp"
	}
	return "tcp"
}

type streamConf struct {
	Url    string `yaml:"url"`
	UseTcp bool   `yaml:"useTcp"`
}

type conf struct {
	Server struct {
		Protocols []string `yaml:"protocols"`
		RtspPort  int      `yaml:"rtspPort"`
		RtpPort   int      `yaml:"rtpPort"`
		RtcpPort  int      `yaml:"rtcpPort"`
	} `yaml:"server"`
	Streams map[string]streamConf `yaml:"streams"`
}

func loadConf(confPath string) (*conf, error) {
	f, err := os.Open(confPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var ret conf
	err = yaml.NewDecoder(f).Decode(&ret)
	if err != nil {
		return nil, err
	}

	return &ret, nil
}

type program struct {
	conf      conf
	protocols map[streamProtocol]struct{}
	mutex     sync.RWMutex
	rtspl     *serverTcpListener
	rtpl      *serverUdpListener
	rtcpl     *serverUdpListener
	clients   map[*client]struct{}
	streams   map[string]*stream
}

func newProgram(confPath string) (*program, error) {
	conf, err := loadConf(confPath)
	if err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	protocols := make(map[streamProtocol]struct{})
	for _, proto := range conf.Server.Protocols {
		switch proto {
		case "udp":
			protocols[_STREAM_PROTOCOL_UDP] = struct{}{}

		case "tcp":
			protocols[_STREAM_PROTOCOL_TCP] = struct{}{}

		default:
			return nil, fmt.Errorf("unsupported protocol: %s", proto)
		}
	}
	if len(protocols) == 0 {
		return nil, fmt.Errorf("no protocols supplied")
	}

	log.Printf("rtsp-simple-proxy %s", Version)

	p := &program{
		conf:      *conf,
		protocols: protocols,
		clients:   make(map[*client]struct{}),
		streams:   make(map[string]*stream),
	}

	p.rtpl, err = newServerUdpListener(p, p.conf.Server.RtpPort, _TRACK_FLOW_RTP)
	if err != nil {
		return nil, err
	}

	p.rtcpl, err = newServerUdpListener(p, p.conf.Server.RtcpPort, _TRACK_FLOW_RTCP)
	if err != nil {
		return nil, err
	}

	p.rtspl, err = newServerTcpListener(p)
	if err != nil {
		return nil, err
	}

	for path, val := range p.conf.Streams {
		var err error
		p.streams[path], err = newStream(p, path, val)
		if err != nil {
			return nil, err
		}
	}

	return p, nil
}

func (p *program) run() {
	go p.rtpl.run()
	go p.rtcpl.run()
	go p.rtspl.run()

	infty := make(chan struct{})
	<-infty
}

func (p *program) forwardTrack(path string, id int, flow trackFlow, frame []byte) {
	for c := range p.clients {
		if c.path == path && c.state == _CLIENT_STATE_PLAY {
			if c.streamProtocol == _STREAM_PROTOCOL_UDP {
				if flow == _TRACK_FLOW_RTP {
					p.rtpl.nconn.WriteTo(frame, &net.UDPAddr{
						IP:   c.ip,
						Port: c.streamTracks[id].rtpPort,
					})
				} else {
					p.rtcpl.nconn.WriteTo(frame, &net.UDPAddr{
						IP:   c.ip,
						Port: c.streamTracks[id].rtcpPort,
					})
				}

			} else {
				c.conn.WriteInterleavedFrame(trackToInterleavedChannel(id, flow), frame)
			}
		}
	}
}

func main() {
	kingpin.CommandLine.Help = "rtsp-simple-proxy " + Version + "\n\n" +
		"RTSP proxy."

	version := kingpin.Flag("version", "print rtsp-simple-proxy version").Bool()
	confPath := kingpin.Arg("confpath", "path of config file").Required().String()

	kingpin.Parse()

	if *version == true {
		fmt.Println("rtsp-simple-proxy " + Version)
		os.Exit(0)
	}

	p, err := newProgram(*confPath)
	if err != nil {
		log.Fatal("ERR: ", err)
	}

	p.run()
}
