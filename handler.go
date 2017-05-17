package nxhttp

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
)

/* entry */
type Entry interface {
	Name() string
	PutData(string, interface{}) Entry

	Processor() NxProcessor

	// chain up processors
	Use(...NxProcessor) Entry

	// add func processor
	Call(func(*NxContext)) Entry

	// test if entry matches given path
	// returns params if matched, otherwise returns nil
	Match(string) []string

	// set timeout for all procs
	SetTimeout(int) Entry

	// execute entry
	Exec(http.ResponseWriter, *http.Request, []string)

	// when entry closed
	Close()
}

type DefaultEntry struct {
	name string
	proc NxProcessor
	data map[string]interface{}
}

func (self *DefaultEntry) Name() string {
	return self.name
}

func (self *DefaultEntry) Processor() NxProcessor {
	return self.proc
}

func (self *DefaultEntry) Use(ps ...NxProcessor) Entry {
	if len(ps) == 0 {
		panic("at least one processor expected")
	}

	tailof := func(p NxProcessor) NxProcessor {
		for ; p.getnext() != nil; p = p.getnext() {
		}
		return p
	}

	if len(ps) > 1 {
		// chain up procs
		for i, p := range ps {
			if i > 0 {
				tailof(ps[i-1]).Then(p)
			}
		}
	}

	if self.proc == nil {
		self.proc = ps[0]
	} else {
		tailof(self.proc).Then(ps[0])
	}

	return self
}

func (self *DefaultEntry) Call(f func(*NxContext)) Entry {
	self.Use(MakeProcessor(f))
	return self
}

func (self *DefaultEntry) Close() {
	if self.proc != nil {
		self.proc.Close()
	}
}

func (self *DefaultEntry) Match(t string) []string {
	return nil
}

func (self *DefaultEntry) SetTimeout(i int) Entry {
	if i > 0 {
		for p := self.proc; p != nil; p = p.getnext() {
			p.SetTimeout(i)
		}
	}
	return self
}

func (self *DefaultEntry) PutData(key string, val interface{}) Entry {
	self.data[key] = val
	return self
}

func (self *DefaultEntry) Exec(w http.ResponseWriter, r *http.Request, params []string) {
	if self.proc != nil {
		ctx := &NxContext{
			res:      w,
			req:      r,
			params:   params,
			datakeys: make([]string, 0),
			cproc:    self.proc,
		}

		// update entry data to context
		for k, v := range self.data {
			ctx.PutData(k, v)
		}

		self.proc.Process(ctx)
	}
}

/* regexp entry */
type RegexpEntry struct {
	DefaultEntry
	re *regexp.Regexp
}

func (self *RegexpEntry) Match(path string) []string {
	ss := self.re.FindAllStringSubmatch(path, -1)
	if len(ss) > 0 {
		params := make([]string, 0)
		for _, s := range ss {
			if len(s) > 1 {
				params = append(params, s[1:]...)
			}
		}
		return params
	}
	return nil
}

func NewRegexpEntry(pattern string, ps ...NxProcessor) *RegexpEntry {
	r := &RegexpEntry{
		DefaultEntry{
			name: pattern,
			data: make(map[string]interface{}),
		},
		regexp.MustCompile(pattern),
	}
	if len(ps) > 0 {
		r.Use(ps...)
	}
	return r
}

/* handler */
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
	default:
		en = nil
		args = nil
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
