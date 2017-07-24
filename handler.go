package nxhttp

import (
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
)

type NxHandler struct {
	getmap  map[string]Entry
	postmap map[string]Entry
	delmap  map[string]Entry
	putmap  map[string]Entry
	mounts  map[string]http.Handler
	timeout int
}

func (self *NxHandler) SetTimeout(ms int) *NxHandler {
	self.timeout = ms
	return self
}

func (self *NxHandler) Close() {
	for _, o := range self.getmap {
		o.Close()
	}
	for _, o := range self.postmap {
		o.Close()
	}
	for _, o := range self.delmap {
		o.Close()
	}
	for _, o := range self.putmap {
		o.Close()
	}
}

func addproc(dict map[string]Entry, pattern string, ps []NxProcessor) Entry {
	if _, ok := dict[pattern]; ok {
		log.Panic(fmt.Sprintf("pattern %q already exists", pattern))
	}
	a := NewRegexpEntry(pattern, ps...)
	dict[pattern] = a
	return a
}

func (self *NxHandler) DoGet(pattern string, ps ...NxProcessor) Entry {
	return addproc(self.getmap, pattern, ps)
}

func (self *NxHandler) DoPost(pattern string, ps ...NxProcessor) Entry {
	return addproc(self.postmap, pattern, ps)
}

func (self *NxHandler) DoDelete(pattern string, ps ...NxProcessor) Entry {
	return addproc(self.delmap, pattern, ps)
}

func (self *NxHandler) DoPut(pattern string, ps ...NxProcessor) Entry {
	return addproc(self.putmap, pattern, ps)
}

func (self *NxHandler) Mount(subpath string, handler http.Handler) {
	if len(subpath) == 0 || subpath == "/" {
		log.Panic(fmt.Sprintf("invalid mount path %q", subpath))
	}
	if !strings.HasSuffix(subpath, "/") {
		subpath = subpath + "/"
	}
	self.mounts[subpath] = http.StripPrefix(subpath, handler)
}

func find(dict map[string]Entry, path string) (Entry, []string) {
	for _, en := range dict {
		if params := en.Match(path); params != nil {
			return en, params
		}
	}
	return nil, nil
}

func (self NxHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if cv := recover(); cv != nil {
			log.Print("****", cv)
			log.Print(string(debug.Stack()))
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(http.StatusText(http.StatusInternalServerError)))
		}
	}()

	// match entry & execute
	var (
		en   Entry
		args []string
	)
	switch r.Method {
	case "GET":
		en, args = find(self.getmap, r.URL.Path)
	case "POST":
		en, args = find(self.postmap, r.URL.Path)
	case "DELETE":
		en, args = find(self.delmap, r.URL.Path)
	case "PUT":
		en, args = find(self.putmap, r.URL.Path)
	case "OPTIONS":
		// when do CORS ajax
		allow := make([]string, 0)
		if u, _ := find(self.getmap, r.URL.Path); u != nil {
			allow = append(allow, "GET")
		}
		if u, _ := find(self.postmap, r.URL.Path); u != nil {
			allow = append(allow, "POST")
		}
		if u, _ := find(self.delmap, r.URL.Path); u != nil {
			allow = append(allow, "DELETE")
		}
		if u, _ := find(self.putmap, r.URL.Path); u != nil {
			allow = append(allow, "PUT")
		}
		if len(allow) > 0 {
			w.Header().Set("access-control-allow-methods", strings.Join(allow, ","))
			// TODO: need to check Origin header value
			w.Header().Set("access-control-allow-origin", r.Header.Get("origin"))
			w.Header().Set("access-control-max-age", "180")
			w.Header().Set("access-control-allow-headers", r.Header.Get("access-control-request-headers"))
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotImplemented)
		}
		return
	}

	if en != nil {
		en.Exec(w, r, args)
		return
	}

	// match subpath
	for sp, h := range self.mounts {
		if strings.HasPrefix(r.URL.Path, sp) {
			h.ServeHTTP(w, r)
			return
		}
	}

	// no match
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
}

func NewNxHandler() *NxHandler {
	r := NxHandler{
		getmap:  make(map[string]Entry),
		postmap: make(map[string]Entry),
		delmap:  make(map[string]Entry),
		putmap:  make(map[string]Entry),
	}
	return &r
}
