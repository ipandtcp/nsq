package protocol

import (
	"net"
	"runtime"
	"strings"

	"github.com/nsqio/nsq/internal/lg"
)

type TCPHandler interface {
	Handle(net.Conn)
}
// 接收一个连接请求，并开启一个 goroutine 并发处理改请求
// 处理工作在handler 里面执行，handler在nsqlookupd Main()里面得到
func TCPServer(listener net.Listener, handler TCPHandler, logf lg.AppLogFunc) {
	logf(lg.INFO, "TCP: listening on %s", listener.Addr())

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Temporary() {
				logf(lg.WARN, "temporary Accept() failure - %s", err)
				runtime.Gosched()
				continue
			}
			// theres no direct way to detect this error because it is not exposed
			if !strings.Contains(err.Error(), "use of closed network connection") {
				logf(lg.ERROR, "listener.Accept() - %s", err)
			}
			break
		}
		go handler.Handle(clientConn)
	}

	logf(lg.INFO, "TCP: closing %s", listener.Addr())
}
