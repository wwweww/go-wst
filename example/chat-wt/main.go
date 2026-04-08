package main

import (
	"flag"
	"log"

	"github.com/wwweww/go-wst/example/internal/chat"
	"github.com/wwweww/go-wst/wt"
	"github.com/zeromicro/go-zero/core/conf"
)

var configFile = flag.String("f", "etc/chat-wt.yaml", "the config file")

func main() {
	flag.Parse()

	var c wt.WtConf
	conf.MustLoad(*configFile, &c)

	handler := chat.NewHandler()
	server := wt.MustNewServer(c, handler)
	defer server.Stop()

	log.Printf("Chat server (WebTransport) starting on %s", c.Addr)
	log.Printf("  https://localhost%s%s", c.Addr, c.Path)
	server.Start()
}
