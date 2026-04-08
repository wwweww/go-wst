package main

import (
	"flag"
	"log"

	"github.com/wwweww/go-wst/example/internal/chat"
	"github.com/wwweww/go-wst/hybrid"
	"github.com/zeromicro/go-zero/core/conf"
)

var configFile = flag.String("f", "etc/chat-hybrid.yaml", "the config file")

func main() {
	flag.Parse()

	var c hybrid.HybridConf
	conf.MustLoad(*configFile, &c)

	handler := chat.NewHandler()
	server := hybrid.MustNewServer(c, handler)
	defer server.Stop()

	log.Printf("Chat server (Hybrid) starting")
	log.Printf("  WebSocket:    ws://localhost%s%s", c.Ws.Addr, c.Ws.Path)
	log.Printf("  WebTransport: https://localhost%s%s", c.Wt.Addr, c.Wt.Path)
	server.Start()
}
