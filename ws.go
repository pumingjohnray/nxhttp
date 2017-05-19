package nxhttp

import (
	"fmt"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"sync"
)

/*
 * Websocket Client & callback
 */
type WebsocketCallback struct {
	OnConnect     func(*WebsocketClient)
	OnMessage     func(*WebsocketClient, []byte)
	OnClose       func(*WebsocketClient)
	OnCheckOrigin func(*http.Request) bool
}

type WebsocketClient struct {
	ctx  *NxContext
	proc *WebsocketProcessor
	conn *websocket.Conn
	send chan []byte
}

func (self *WebsocketClient) Conn() *websocket.Conn {
	return self.conn
}

func (self *WebsocketClient) Send(msg []byte) {
	if self.IsDebug() {
		fmt.Println("[ws-send]", msg)
	}
	self.send <- msg
}

func (self *WebsocketClient) Broadcast(msg []byte) {
	self.proc.broadcast(msg)
}

func (self *WebsocketClient) PutData(key string, val interface{}) {
	self.ctx.PutData(key, val)
}

func (self *WebsocketClient) GetData(key string) interface{} {
	return self.ctx.GetData(key)
}

func (self *WebsocketClient) IsDebug() bool {
	return self.ctx.IsDebug()
}

func (self *WebsocketClient) IsAlive() bool {
	return self.send != nil
}

func (self *WebsocketClient) start() {
	if self.IsDebug() {
		fmt.Println("[ws-start] ", self)
	}

	if self.proc.callbacks != nil && self.proc.callbacks.OnConnect != nil {
		self.proc.callbacks.OnConnect(self)
	}

	// start reader
	go func(cli *WebsocketClient) {
		defer cli.stop()
		for {
			if _, msg, err := cli.conn.ReadMessage(); err != nil {
				log.Println(err)
				break
			} else {
				if self.IsDebug() {
					fmt.Println("[ws-recv] ", msg)
				}
				if cli.proc.callbacks != nil && cli.proc.callbacks.OnMessage != nil {
					cli.proc.callbacks.OnMessage(cli, msg)
				}
			}
		}
	}(self)

	// start writer
	go func(cli *WebsocketClient) {
		defer cli.stop()
		for {
			select {
			case message, ok := <-cli.send:
				if !ok {
					cli.conn.WriteMessage(websocket.CloseMessage, []byte{})
					break
				} else {
					if cli.IsDebug() {
						fmt.Println("[ws-send] ", message)
					}
					cli.conn.WriteMessage(websocket.TextMessage, []byte(message))
				}
			}
		}
	}(self)
}

func (self *WebsocketClient) stop() {
	if self.IsAlive() {
		if self.IsDebug() {
			fmt.Println("[ws-stop]", self)
		}

		self.proc.removeClient(self)

		if self.proc.callbacks != nil && self.proc.callbacks.OnClose != nil {
			self.proc.callbacks.OnClose(self)
		}

		close(self.send)
		self.conn.Close()

		// to mark client is gone
		self.send = nil
	}
}

/*
 * websocket processor
 */
type WebsocketProcessor struct {
	DefaultProcessor
	bufsize   int
	callbacks *WebsocketCallback
	clients   map[*WebsocketClient]bool
	lock      sync.RWMutex
}

func (self *WebsocketProcessor) removeClient(cli *WebsocketClient) {
	self.lock.Lock()
	defer self.lock.Unlock()

	if _, ok := self.clients[cli]; ok {
		delete(self.clients, cli)
	}
}

func (self *WebsocketProcessor) broadcast(msg []byte) {
	fails := make([]*WebsocketClient, 0)
	{
		self.lock.RLock()
		defer self.lock.RUnlock()
		for cli := range self.clients {
			select {
			case cli.send <- msg:
			default: // fail sending msg to cli
				fails = append(fails, cli)
			}
		}
	}

	if len(fails) > 0 {
		// close failed clients
		for _, c := range fails {
			c.stop()
		}
	}
}

func (self *WebsocketProcessor) Close() {
	for c := range self.clients {
		c.stop()
		delete(self.clients, c)
	}
	self.DefaultProcessor.Close()
}

func (self *WebsocketProcessor) Process(ctx *NxContext) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  self.bufsize,
		WriteBufferSize: self.bufsize,
	}
	if self.callbacks != nil {
		upgrader.CheckOrigin = self.callbacks.OnCheckOrigin
	}

	if conn, err := upgrader.Upgrade(ctx.res, ctx.req, nil); err == nil {
		cli := &WebsocketClient{
			ctx:  ctx,
			proc: self,
			conn: conn,
			send: make(chan []byte),
		}

		self.lock.Lock()
		self.clients[cli] = true
		self.lock.Unlock()

		cli.start()
		ctx.RunNext()
	} else {
		log.Print(err)
		ctx.End(http.StatusNotAcceptable)
	}
}

type WSEntry struct {
	RegexpEntry
}

func (self *WSEntry) SetCallback(c *WebsocketCallback) *WSEntry {
	for p := self.Processor(); p != nil; p = p.getnext() {
		switch p.(type) {
		case *WebsocketProcessor:
			p.(*WebsocketProcessor).callbacks = c
		}
	}
	return self
}

/* handler methods for ws */
func (self *NxHandler) Websocket(pattern string, ps ...NxProcessor) *WSEntry {
	if _, ok := self.getmap[pattern]; ok {
		panic(fmt.Sprintf("pattern %q exists", pattern))
	}

	p := &WebsocketProcessor{
		DefaultProcessor: DefaultProcessor{
			name: "websocket",
		},
		bufsize: 256,
		clients: make(map[*WebsocketClient]bool),
		lock:    sync.RWMutex{},
	}

	en := &WSEntry{
		*NewRegexpEntry(pattern, append(ps, p)...),
	}
	self.getmap[pattern] = en
	return en
}
