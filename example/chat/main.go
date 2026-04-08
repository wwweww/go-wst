package main

import (
	"flag"
	"log"

	"github.com/wwweww/go-wst/example/internal/chat"
	"github.com/wwweww/go-wst/ws"
	"github.com/zeromicro/go-zero/core/conf"
)

var configFile = flag.String("f", "etc/chat.yaml", "the config file")

func main() {
	flag.Parse()

	var c ws.WsConf
	conf.MustLoad(*configFile, &c)

	handler := chat.NewHandler()
	server := ws.MustNewServer(c, handler)

	log.Printf("Chat server (WebSocket) starting on %s", c.Addr)
	log.Printf("  ws://localhost%s%s", c.Addr, c.Path)
	server.Start()
}
