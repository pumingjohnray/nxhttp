package nxhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type NxContext struct {
	req      *http.Request
	res      http.ResponseWriter
	params   []string
	datakeys []string
	cproc    NxProcessor // current proc
	stopped  bool        // if stopped proc chainning
	debug    bool
}

func (self *NxContext) Req() *http.Request {
	return self.req
}

func (self *NxContext) Res() http.ResponseWriter {
	return self.res
}

func (self *NxContext) Header(key string) string {
	return self.req.Header.Get(key)
}

func (self *NxContext) Cookie(key string) (*http.Cookie, error) {
	return self.req.Cookie(key)
}

func (self *NxContext) UrlParams() []string {
	return self.params
}

func (self *NxContext) UrlParam(idx int) string {
	if idx < len(self.params) {
		return self.params[idx]
	} else {
		return ""
	}
}

func (self *NxContext) FormValue(name string) string {
	return self.req.FormValue(name)
}

func (self *NxContext) FormValueInt(name string, failsafe int) int {
	v := self.FormValue(name)
	if i, e := strconv.ParseInt(v, 10, 32); e != nil {
		return failsafe
	} else {
		return int(i)
	}
}

func (self *NxContext) FormValueBool(name string, failsafe bool) bool {
	v := strings.ToLower(self.FormValue(name))
	switch v {
	case "yes", "y", "true", "t", "1":
		return true
	case "no", "n", "false", "f", "0":
		return false
	case "":
		return failsafe
	default:
		if b, e := strconv.ParseBool(v); e != nil {
			return failsafe
		} else {
			return b
		}
	}
}

func (self *NxContext) SetDebug(b bool) *NxContext {
	self.debug = b
	return self
}

func (self *NxContext) IsDebug() bool {
	return self.debug
}

func (self *NxContext) PutData(k string, v interface{}) *NxContext {
	exists := false
	for _, x := range self.datakeys {
		if x == k {
			exists = true
			break
		}
	}
	if !exists {
		self.datakeys = append(self.datakeys, k)
	}

	self.req = self.req.WithContext(context.WithValue(self.req.Context(), k, v))
	return self
}

func (self *NxContext) GetData(k string) interface{} {
	return self.req.Context().Value(k)
}

func (self *NxContext) DataNames() []string {
	return self.datakeys
}

func (self *NxContext) IsAjax() bool {
	return strings.ToLower(self.req.Header.Get("X-Requested-With")) == "xmlhttprequest"
}

func (self *NxContext) SendBytes(b []byte) *NxContext {
	self.res.Write(b)
	return self
}

func (self *NxContext) SendString(text string) *NxContext {
	self.res.Write([]byte(text))
	return self
}

func (self *NxContext) SendAsJson(o interface{}) *NxContext {
	self.res.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(self.res)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(o); err != nil {
		panic(err)
	} else {
		return self
	}
}

func (self *NxContext) RunNext() {
	if self.cproc != nil && !self.stopped {
		if p := self.cproc.getnext(); p != nil {
			self.cproc = p
			p.Process(self)
		}
	}
}

func (self *NxContext) End(status int) {
	if !self.stopped {
		self.stopped = true
		self.res.WriteHeader(status)
	}
}

func (self *NxContext) IsStopped() bool {
	return self.stopped
}

func (self *NxContext) Redirect(url string) {
	if !self.stopped {
		self.stopped = true
		http.Redirect(self.Res(), self.Req(), "/ui/signin", http.StatusMovedPermanently)
	}
}
