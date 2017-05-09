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
	proc *WSProcessor
	conn *websocket.Conn
	send chan []byte
	data map[string]interface{}
}

func (self *WebsocketClient) Conn() *websocket.Conn {
	return self.conn
}

func (self *WebsocketClient) Send(msg []byte) {
	self.send <- msg
}

func (self *WebsocketClient) Broadcast(msg []byte) {
	self.proc.broadcast(msg)
}

func (self *WebsocketClient) Close() {
	self.proc.remove(self)
	self.data = make(map[string]interface{})

	if self.send != nil {
		if self.proc.callbacks != nil && self.proc.callbacks.OnClose != nil {
			self.proc.callbacks.OnClose(self)
		}

		close(self.send)
		self.conn.Close()

		// to mark client is already closed
		self.send = nil
	}
}

func (self *WebsocketClient) PutData(key string, val interface{}) {
	self.data[key] = val
}

func (self *WebsocketClient) GetData(key string) interface{} {
	if v, ok := self.data[key]; ok {
		return v
	} else {
		return nil
	}
}

func loopWrite(cli *WebsocketClient) {
	defer cli.Close()
	for {
		select {
		case message, ok := <-cli.send:
			if !ok {
				cli.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			} else {
				log.Print("[ws-send] ", message)
				cli.conn.WriteMessage(websocket.TextMessage, []byte(message))
			}
		}
	}
}

func loopRead(cli *WebsocketClient) {
	defer cli.Close()
	for {
		if _, msg, err := cli.conn.ReadMessage(); err != nil {
			log.Print(err)
			break
		} else if cli.proc.callbacks != nil && cli.proc.callbacks.OnMessage != nil {
			cli.proc.callbacks.OnMessage(cli, msg)
		}
	}
}

/*
 * websocket processor
 */
type WSProcessor struct {
	DefaultProcessor
	bufsize   int
	callbacks *WebsocketCallback
	clients   map[*WebsocketClient]bool
	lock      sync.RWMutex
}

func (self *WSProcessor) remove(cli *WebsocketClient) {
	self.lock.Lock()
	if _, ok := self.clients[cli]; ok {
		delete(self.clients, cli)
	}
	self.lock.Unlock()
}

func (self *WSProcessor) broadcast(msg []byte) {
	self.lock.RLock()
	for cli := range self.clients {
		select {
		case cli.send <- msg:
		default:
			cli.Close()
		}
	}
	self.lock.RUnlock()
}

func (self *WSProcessor) Close() {
	self.lock.Lock()
	for c := range self.clients {
		c.Close()
		delete(self.clients, c)
	}
	self.lock.Unlock()

	self.DefaultProcessor.Close()
}

func (self *WSProcessor) Process(ctx *NxContext) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  self.bufsize,
		WriteBufferSize: self.bufsize,
	}
	if self.callbacks != nil {
		upgrader.CheckOrigin = self.callbacks.OnCheckOrigin
	}

	if conn, err := upgrader.Upgrade(ctx.res, ctx.req, nil); err == nil {
		c := &WebsocketClient{
			proc: self,
			conn: conn,
			send: make(chan []byte),
			data: make(map[string]interface{}),
		}
		for _, x := range ctx.DataNames() {
			c.data[x] = ctx.GetData(x)
		}
		if len(ctx.UrlParams()) > 0 {
			c.data["url_params"] = ctx.UrlParams()[:]
		}

		self.lock.Lock()
		self.clients[c] = true
		self.lock.Unlock()
		c.start()
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
		case *WSProcessor:
			p.(*WSProcessor).callbacks = c
		}
	}
	return self
}

/* handler methods for ws */
func (self *NxHandler) Websocket(pattern string, ps ...NxProcessor) *WSEntry {
	if _, ok := self.getmap[pattern]; ok {
		panic(fmt.Sprintf("pattern %q exists", pattern))
	}

	p := &WSProcessor{
		DefaultProcessor: DefaultProcessor{
			name: "websocket",
		},
		bufsize: 512,
		clients: make(map[*WebsocketClient]bool),
		lock:    sync.RWMutex{},
	}

	en := &WSEntry{
		*NewRegexpEntry(pattern, append(ps, p)...),
	}
	self.getmap[pattern] = en
	return en
}

func (self *WebsocketClient) start() {
	if self.proc.callbacks != nil && self.proc.callbacks.OnConnect != nil {
		self.proc.callbacks.OnConnect(self)
	}

	go loopRead(self)
	go loopWrite(self)
}
