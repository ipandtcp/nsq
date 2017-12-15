package http_api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/nsqio/nsq/internal/lg"
)

type Decorator func(APIHandler) APIHandler

type APIHandler func(http.ResponseWriter, *http.Request, httprouter.Params) (interface{}, error)

type Err struct {
	Code int
	Text string
}

func (e Err) Error() string {
	return e.Text
}

func acceptVersion(req *http.Request) int {
	if req.Header.Get("accept") == "application/vnd.nsq; version=1.0" {
		return 1
	}

	return 0
}

func PlainText(f APIHandler) APIHandler {
	return func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
		code := 200
		data, err := f(w, req, ps)
		if err != nil {
			code = err.(Err).Code
			data = err.Error()
		}
		switch d := data.(type) {
		case string:
			w.WriteHeader(code)
			io.WriteString(w, d)
		case []byte:
			w.WriteHeader(code)
			w.Write(d)
		default:
			panic(fmt.Sprintf("unknown response type %T", data))
		}
		return nil, nil
	}
}

// Version 1 的接口响应函数
// 用于包装一层APIHandler， 执行被包裹的APIHanler, 对接口做相应的响应，
func V1(f APIHandler) APIHandler {
	return func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
		data, err := f(w, req, ps)
		if err != nil {
			RespondV1(w, err.(Err).Code, err)
			return nil, nil
		}
		RespondV1(w, 200, data)
		return nil, nil
	}
}

func RespondV1(w http.ResponseWriter, code int, data interface{}) {
	var response []byte
	var err error
	var isJSON bool

	if code == 200 {
		switch data.(type) {
		case string:
			response = []byte(data.(string))
		case []byte:
			response = data.([]byte)
		case nil:
			response = []byte{}
		default:
			isJSON = true
			response, err = json.Marshal(data)
			if err != nil {
				code = 500
				data = err
			}
		}
	}

	if code != 200 {
		isJSON = true
		response = []byte(fmt.Sprintf(`{"message":"%s"}`, data))
	}

	if isJSON {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.Header().Set("X-NSQ-Content-Type", "nsq; version=1.0")
	w.WriteHeader(code)
	w.Write(response)
}

// 此处的写法还是有些难度的
// 以nsqlookupd_http.go 为列:
//   在调用的地方，f 是接口处理函数，接下来的是Decorator类型的参数
//   第二个参数是日志处理函数，通过下面的Log函数以闭包的方式返回，第三个参数的本文件的V1函数也是Decorator
//   而Decorator 接收APIHandler作为参数，并返回APIHandler类型
//   因此，第一个参数f 可以作为每个传入的Decorator参数的参数，并且会返回一个APIHandler类型的返回值可以赋值给decorated
//   因此，for里面的第一回合：接口处理函数（第一个参数f）作为Log（第二个参数ds[0]）的参数，就被包裹了一层，等于是被它调用
//      for里面的第二回合：被包裹一层的f作为参数给V1再包裹一层
//   最后返回的是被包裹了两层的Handle
// 总结：其实这个函数就是用后面的参数去包裹第一个APIHandler, 因此叫Decorate(装饰)
func Decorate(f APIHandler, ds ...Decorator) httprouter.Handle {
	decorated := f
	for _, decorate := range ds {
		decorated = decorate(decorated)
	}
	return func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		decorated(w, req, ps)
	}
}

func Log(logf lg.AppLogFunc) Decorator {
	return func(f APIHandler) APIHandler {
		return func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
			start := time.Now()
			response, err := f(w, req, ps)
			elapsed := time.Since(start)
			status := 200
			if e, ok := err.(Err); ok {
				status = e.Code
			}
			logf(lg.INFO, "%d %s %s (%s) %s",
				status, req.Method, req.URL.RequestURI(), req.RemoteAddr, elapsed)
			return response, err
		}
	}
}

// 同下面的LogNotFoundHandler
func LogPanicHandler(logf lg.AppLogFunc) func(w http.ResponseWriter, req *http.Request, p interface{}) {
	return func(w http.ResponseWriter, req *http.Request, p interface{}) {
		logf(lg.ERROR, "panic in HTTP handler - %s", p)
		Decorate(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
			return nil, Err{500, "INTERNAL_ERROR"}
		}, Log(logf), V1)(w, req, nil)
	}
}

// 返回一个Handler, 强制转换匿名函数为http.HanderFunc, 至于Handler与HandlerFunc的关系，可以看看http包的源代码，
// 接下来是Decorate 第一个参数，匿名函数，返回404，第二个参数是本文件的Log函数包装，第三个是本文件的V1函数，具体看Decorate函数的注释
// Decorate 执行后返回一个APIHandler，后面直接跟(w, req, nil) 就调用了该APIHandler，这里需要注意，一不小心就翻车了
// 当然不用担心APIHandler的返回值被丢弃，因为是被log,V1 包裹了两层，具体看Decorate
func LogNotFoundHandler(logf lg.AppLogFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		Decorate(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
			return nil, Err{404, "NOT_FOUND"}
		}, Log(logf), V1)(w, req, nil)
	})
}

// 同上面的LogNotFoundHandler
func LogMethodNotAllowedHandler(logf lg.AppLogFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		Decorate(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
			return nil, Err{405, "METHOD_NOT_ALLOWED"}
		}, Log(logf), V1)(w, req, nil)
	})
}
