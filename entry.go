package nxhttp

import (
	"net/http"
	"regexp"
)

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

	// debug switch
	SetDebug(bool) Entry
	IsDebug() bool

	// execute entry
	Exec(http.ResponseWriter, *http.Request, []string)

	// when entry closed
	Close()
}

type BaseEntry struct {
	name  string
	proc  NxProcessor
	data  map[string]interface{}
	debug bool
}

func (self *BaseEntry) Name() string {
	return self.name
}

func (self *BaseEntry) Processor() NxProcessor {
	return self.proc
}

func (self *BaseEntry) Use(ps ...NxProcessor) Entry {
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

func (self *BaseEntry) Call(f func(*NxContext)) Entry {
	self.Use(MakeProcessor(f))
	return self
}

func (self *BaseEntry) Close() {
	if self.proc != nil {
		self.proc.Close()
	}
}

func (self *BaseEntry) Match(t string) []string {
	return nil
}

func (self *BaseEntry) SetTimeout(i int) Entry {
	if i > 0 {
		for p := self.proc; p != nil; p = p.getnext() {
			p.SetTimeout(i)
		}
	}
	return self
}

func (self *BaseEntry) SetDebug(b bool) Entry {
	self.debug = b
	return self
}

func (self *BaseEntry) IsDebug() bool {
	return self.debug
}

func (self *BaseEntry) PutData(key string, val interface{}) Entry {
	self.data[key] = val
	return self
}

func (self *BaseEntry) Exec(w http.ResponseWriter, r *http.Request, params []string) {
	if self.proc != nil {
		ctx := &NxContext{
			res:      w,
			req:      r,
			params:   params,
			datakeys: make([]string, 0),
			cproc:    self.proc,
			debug:    self.IsDebug(),
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
	BaseEntry
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
		BaseEntry{
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
