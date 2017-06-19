package nxhttp

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type CgiProcessor struct {
	DefaultProcessor
	bin  string
	opts []string
	envs []string
}

func (self *CgiProcessor) Process(ctx *NxContext) {
	r := ctx.Req()
	w := ctx.Res()

	// make env
	env := self.envs[:]
	env = append(env, "SERVER_PROTOCOL=HTTP/1.1")
	env = append(env, "GATEWAY_INTERFACE=CGI/1.1")
	env = append(env, fmt.Sprintf("PATH_INFO=%s", r.URL.Path))
	env = append(env, fmt.Sprintf("REQUEST_METHOD=%s", r.Method))
	env = append(env, fmt.Sprintf("QUERY_STRING=%s", r.URL.RawQuery))
	env = append(env, fmt.Sprintf("CONTENT_LENGTH=%d", r.ContentLength))

	hp := strings.Split(r.Host, ":")
	env = append(env, fmt.Sprintf("SERVER_NAME=%s", hp[0]))
	if len(hp) > 1 {
		env = append(env, fmt.Sprintf("SERVER_PORT=%s", hp[1]))
	} else {
		env = append(env, fmt.Sprintf("SERVER_PORT=80"))
	}

	for k, vs := range r.Header {
		for _, s := range vs {
			name := strings.Replace(strings.ToUpper(k), "-", "_", -1)
			env = append(env, fmt.Sprintf("%s=%s", name, s))
			env = append(env, fmt.Sprintf("HTTP_%s=%s", name, s))
		}
	}

	// make cmd options
	args := self.opts[:]
	if oo := ctx.GetData("cgi:options"); oo != nil {
		if ss, ok := reflect.ValueOf(oo).Interface().([]string); ok {
			args = append(args, ss...)
		}
	}
	for _, v := range ctx.UrlParams() {
		args = append(args, v)
	}

	if ctx.IsDebug() {
		fmt.Println("[CGI] ", self.bin, args)
	}

	var cmd *exec.Cmd
	if self.GetTimeout() > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(self.GetTimeout())*time.Millisecond)
		defer cancel()
		cmd = exec.CommandContext(ctx, self.bin, args...)
	} else {
		cmd = exec.Command(self.bin, args...)
	}
	cmd.Env = env

	stdin, erri := cmd.StdinPipe()
	if erri != nil {
		log.Print(erri)
		ctx.End(http.StatusInternalServerError)
		return
	}

	stdout, erro := cmd.StdoutPipe()
	if erro != nil {
		log.Print(erro)
		ctx.End(http.StatusInternalServerError)
		return
	}

	stderr, erre := cmd.StderrPipe()
	if erre != nil {
		log.Print(erre)
		ctx.End(http.StatusInternalServerError)
		return
	}

	// stdin feeding routine
	go func() {
		defer stdin.Close()

		buf := make([]byte, 512)
		for {
			if n, e := r.Body.Read(buf); e != nil {
				if n > 0 {
					stdin.Write(buf[:n])
				}
				break
			} else {
				stdin.Write(buf[:n])
			}
		}
	}()

	// stdout piping routine
	go func() {
		defer stdout.Close()

		buf := make([]byte, 512)
		eoh, _ := regexp.Compile(`\r?\n\r?\n`)

		isheader := true
		status := 200
		hdr := make([]byte, 0)
		status_re := regexp.MustCompile(`^HTTP/.+(\d\d\d)`)

		for stop := false; !stop; {
			n, e := stdout.Read(buf)
			if e != nil {
				stop = true
			}

			if n > 0 {
				if isheader {
					if idx := eoh.FindIndex(buf); idx != nil {
						//parse header
						hdr = append(hdr, buf[:idx[0]]...)
						isheader = false

						for _, s := range strings.Split(string(hdr), "\n") {
							if s[len(s)-1] == '\r' {
								s = s[:len(s)-1]
							}

							p := strings.SplitN(s, ":", 2)
							if len(p) > 1 {
								name := strings.Trim(p[0], " ")
								val := strings.Trim(p[1], " ")
								if strings.ToLower(name) == "status" {
									if x, err := strconv.Atoi(val); err == nil {
										status = x
									}
								} else {
									w.Header().Set(name, val)
								}
							} else {
								// check http status, e.g.: HTTP/1.1 200 OK
								if t := status_re.FindAllStringSubmatch(s, -1); len(t) > 0 {
									x, _ := strconv.ParseInt(t[0][1], 10, 16)
									status = int(x)
								}
							}
						}

						if !ctx.IsStopped() {
							// send header and body
							w.WriteHeader(status)
							if idx[1] < n-1 {
								if _, e := w.Write(buf[idx[1]:n]); e != nil {
									log.Println(e)
									stop = true
								}
							}
						}
					} else {
						hdr = append(hdr, buf[:n]...)
					}
				} else {
					// send body to client
					if !ctx.IsStopped() {
						if _, e := w.Write(buf[:n]); e != nil {
							log.Println(e)
							stop = true
						}
					}
				}
			}
		}
	}()

	// stderr piping routine
	go func() {
		defer stderr.Close()

		buf := make([]byte, 512)
		for {
			n, e := stderr.Read(buf)
			if e != nil {
				if n > 0 {
					log.Print(string(buf[:n]))
				}
				break
			} else {
				log.Print(string(buf[:n]))
			}
		}
	}()

	if err := cmd.Run(); err != nil {
		log.Print("cgi exec error: ", err)
		ctx.End(http.StatusInternalServerError)
	} else {
		ctx.RunNext()
	}
}

func NewCgiProcessor(bin string, opts []string, envmap map[string]string) *CgiProcessor {
	envs := make([]string, 0)
	if envmap != nil && len(envmap) > 0 {
		for k, v := range envmap {
			envs = append(envs, fmt.Sprintf("%s=%s", k, v))
		}
	}

	p := &CgiProcessor{
		DefaultProcessor: DefaultProcessor{
			name: "cgi",
		},
		bin:  bin,
		opts: opts,
		envs: envs,
	}
	return p
}

func addcgi(dict map[string]Entry, pattern, bin string, args ...interface{}) Entry {
	if _, ok := dict[pattern]; ok {
		log.Panic(fmt.Sprintf("pattern %q already exists", pattern))
	}

	opts := make([]string, 0)
	envs := make(map[string]string)
	procs := make([]NxProcessor, 0)
	wantproc := false

	for _, i := range args {
		switch i.(type) {
		case []string:
			if wantproc {
				log.Panicf("invalid cgi-processor argument %q. NxProcessor expexted", i)
			}
			opts = append(opts, i.([]string)...)
		case map[string]string:
			if wantproc {
				log.Panicf("invalid cgi-processor argument %q. NxProcessor expexted", i)
			}
			for k, v := range i.(map[string]string) {
				envs[k] = v
			}
		case NxProcessor:
			wantproc = true
			procs = append(procs, i.(NxProcessor))
		default:
			log.Panicf("invalid argument ", i)
		}
	}

	a := NewRegexpEntry(pattern, append(procs, NewCgiProcessor(bin, opts, envs))...)
	dict[pattern] = a
	return a
}

func (self *NxHandler) DoCgiGet(pattern, bin string, args ...interface{}) Entry {
	return addcgi(self.getmap, pattern, bin, args...)
}

func (self *NxHandler) DoCgiPost(pattern, bin string, args ...interface{}) Entry {
	return addcgi(self.postmap, pattern, bin, args...)
}

func (self *NxHandler) DoCgiDelete(pattern, bin string, args ...interface{}) Entry {
	return addcgi(self.delmap, pattern, bin, args...)
}

func (self *NxHandler) DoCgiPut(pattern, bin string, args ...interface{}) Entry {
	return addcgi(self.putmap, pattern, bin, args...)
}
