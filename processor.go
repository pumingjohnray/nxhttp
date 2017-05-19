package nxhttp

import (
	"database/sql"
	"log"
	"net/http"
)

type NxProcessor interface {
	Name() string

	// processor timeout
	GetTimeout() int
	SetTimeout(int) NxProcessor

	// main entry
	Process(*NxContext)

	// next chained processor
	getnext() NxProcessor

	// add next processor
	Then(NxProcessor) NxProcessor

	Close()
}

type DefaultProcessor struct {
	name    string
	timeout int
	next    NxProcessor
}

func (self *DefaultProcessor) Name() string {
	return self.name
}

func (self *DefaultProcessor) Close() {
	if self.next != nil {
		self.next.Close()
	}
}

func (self *DefaultProcessor) GetTimeout() int {
	return self.timeout
}

func (self *DefaultProcessor) SetTimeout(i int) NxProcessor {
	if i > 0 {
		self.timeout = i
	}
	return self
}

func (self *DefaultProcessor) Then(p NxProcessor) NxProcessor {
	if self.next != nil {
		log.Panicf("aleady has next processor ", self, self.next)
	}
	self.next = p
	return p
}

func (self *DefaultProcessor) getnext() NxProcessor {
	return self.next
}

func (self *DefaultProcessor) Process(ctx *NxContext) {
	panic("DefaultProcessor.Process() is supposed to be overriden")
}

func NewDefaultProcessor(name string) *DefaultProcessor {
	return &DefaultProcessor{
		name: name,
	}
}

/* function processor */
type fnProc struct {
	DefaultProcessor
	fn func(*NxContext)
}

func (self *fnProc) Process(ctx *NxContext) {
	self.fn(ctx)
}

func MakeProcessor(fs ...func(*NxContext)) NxProcessor {
	var last, root NxProcessor
	for i, f := range fs {
		p := &fnProc{
			DefaultProcessor{name: "function"},
			f,
		}
		if i == 0 {
			root = p
			last = p
		} else {
			last.Then(p)
			last = p
		}
	}
	return root
}

/*
 * builtin processors
 */

func NewLoggingProc() NxProcessor {
	return MakeProcessor(func(ctx *NxContext) {
		log.Printf("[%s] %q", ctx.Req().Method, ctx.Req().URL.Path)
		ctx.RunNext()
	})
}

// database transaction begin/commit processor
type DbTx struct {
	DefaultProcessor
	db     *sql.DB
	commit bool
}

func (self *DbTx) Process(ctx *NxContext) {
	if tx, e := self.db.Begin(); e != nil {
		log.Print(e)
		ctx.End(http.StatusInternalServerError)
	} else {
		defer tx.Rollback()
		ctx.PutData("_dbtx", tx).RunNext()
		if self.commit {
			tx.Commit()
		}
	}
}

func NewDbTx(db *sql.DB, commit bool) *DbTx {
	p := &DbTx{
		DefaultProcessor{name: "dbtransx"},
		db,
		commit,
	}
	return p
}
