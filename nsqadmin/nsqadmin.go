package nsqadmin

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"

	"github.com/nsqio/nsq/internal/http_api"
	"github.com/nsqio/nsq/internal/lg"
	"github.com/nsqio/nsq/internal/util"
	"github.com/nsqio/nsq/internal/version"
)

type NSQAdmin struct {
	sync.RWMutex

	// opts 保存的是配置信息，通过原子操作来解决同步的问题
	opts atomic.Value

	httpListener        net.Listener
	waitGroup           util.WaitGroupWrapper
	notifications       chan *AdminAction
	graphiteURL         *url.URL
	httpClientTLSConfig *tls.Config
}

// 调用该方法之前，需要先New一个Options, opt := NewOptions()
// 这个函数就是配置好nsqadmin的运行环境
func New(opts *Options) *NSQAdmin {
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stderr, opts.LogPrefix, log.Ldate|log.Ltime|log.Lmicroseconds)
	}

	n := &NSQAdmin{
		notifications: make(chan *AdminAction),
	}
	//这里是把Options 的配置信息储存到n.opts中
	n.swapOpts(opts)

	var err error

	// LogLevel是日志级别的string, logLevel是封装过的int,内部使用的，注意大小写,不要以为是同一个
	opts.logLevel, err = lg.ParseLogLevel(opts.LogLevel, opts.Verbose)
	if err != nil {
		n.logf(LOG_FATAL, "%s", err)
		os.Exit(1)
	}

	// nsqd 不能与lookupd地址同时指定
	if len(opts.NSQDHTTPAddresses) == 0 && len(opts.NSQLookupdHTTPAddresses) == 0 {
		n.logf(LOG_FATAL, "--nsqd-http-address or --lookupd-http-address required.")
		os.Exit(1)
	}

	if len(opts.NSQDHTTPAddresses) != 0 && len(opts.NSQLookupdHTTPAddresses) != 0 {
		n.logf(LOG_FATAL, "use --nsqd-http-address or --lookupd-http-address not both")
		os.Exit(1)
	}

	// verify that the supplied address is valid
	verifyAddress := func(arg string, address string) *net.TCPAddr {
		addr, err := net.ResolveTCPAddr("tcp", address)
		if err != nil {
			n.logf(LOG_FATAL, "failed to resolve %s address (%s) - %s", arg, address, err)
			os.Exit(1)
		}
		return addr
	}

	// 如果指定了https证书，使用它们
	if opts.HTTPClientTLSCert != "" && opts.HTTPClientTLSKey == "" {
		n.logf(LOG_FATAL, "--http-client-tls-key must be specified with --http-client-tls-cert")
		os.Exit(1)
	}

	if opts.HTTPClientTLSKey != "" && opts.HTTPClientTLSCert == "" {
		n.logf(LOG_FATAL, "--http-client-tls-cert must be specified with --http-client-tls-key")
		os.Exit(1)
	}

	n.httpClientTLSConfig = &tls.Config{
		InsecureSkipVerify: opts.HTTPClientTLSInsecureSkipVerify,
	}
	if opts.HTTPClientTLSCert != "" && opts.HTTPClientTLSKey != "" {
		cert, err := tls.LoadX509KeyPair(opts.HTTPClientTLSCert, opts.HTTPClientTLSKey)
		if err != nil {
			n.logf(LOG_FATAL, "failed to LoadX509KeyPair %s, %s - %s",
				opts.HTTPClientTLSCert, opts.HTTPClientTLSKey, err)
			os.Exit(1)
		}
		n.httpClientTLSConfig.Certificates = []tls.Certificate{cert}
	}
	if opts.HTTPClientTLSRootCAFile != "" {
		tlsCertPool := x509.NewCertPool()
		caCertFile, err := ioutil.ReadFile(opts.HTTPClientTLSRootCAFile)
		if err != nil {
			n.logf(LOG_FATAL, "failed to read TLS root CA file %s - %s",
				opts.HTTPClientTLSRootCAFile, err)
			os.Exit(1)
		}
		if !tlsCertPool.AppendCertsFromPEM(caCertFile) {
			n.logf(LOG_FATAL, "failed to AppendCertsFromPEM %s", opts.HTTPClientTLSRootCAFile)
			os.Exit(1)
		}
		n.httpClientTLSConfig.RootCAs = tlsCertPool
	}

	// require that both the hostname and port be specified
	for _, address := range opts.NSQLookupdHTTPAddresses {
		verifyAddress("--lookupd-http-address", address)
	}

	for _, address := range opts.NSQDHTTPAddresses {
		verifyAddress("--nsqd-http-address", address)
	}

	if opts.ProxyGraphite {
		url, err := url.Parse(opts.GraphiteURL)
		if err != nil {
			n.logf(LOG_FATAL, "failed to parse --graphite-url='%s' - %s", opts.GraphiteURL, err)
			os.Exit(1)
		}
		n.graphiteURL = url
	}

	if opts.AllowConfigFromCIDR != "" {
		_, _, err := net.ParseCIDR(opts.AllowConfigFromCIDR)
		if err != nil {
			n.logf(LOG_FATAL, "failed to parse --allow-config-from-cidr='%s' - %s", opts.AllowConfigFromCIDR, err)
			os.Exit(1)
		}
	}

	n.logf(LOG_INFO, version.String("nsqadmin"))

	return n
}

func (n *NSQAdmin) getOpts() *Options {
	return n.opts.Load().(*Options)
}

func (n *NSQAdmin) swapOpts(opts *Options) {
	n.opts.Store(opts)
}

func (n *NSQAdmin) RealHTTPAddr() *net.TCPAddr {
	n.RLock()
	defer n.RUnlock()
	return n.httpListener.Addr().(*net.TCPAddr)
}

// http的handle如果被合法请求，有些会推送一个action，这边接受到action,就往 --notification end point post 相关消息。如果没有，就堵塞
// 官方解释：If the --notification-http-endpoint flag is set,
//   nsqadmin will send a POST request to the specified (fully qualified) endpoint each time an admin action (such as pausing a channel) is performed.
func (n *NSQAdmin) handleAdminActions() {
	for action := range n.notifications {
		content, err := json.Marshal(action)
		if err != nil {
			n.logf(LOG_ERROR, "failed to serialize admin action - %s", err)
		}
		httpclient := &http.Client{
			Transport: http_api.NewDeadlineTransport(n.getOpts().HTTPClientConnectTimeout, n.getOpts().HTTPClientRequestTimeout),
		}
		n.logf(LOG_INFO, "POSTing notification to %s", n.getOpts().NotificationHTTPEndpoint)
		resp, err := httpclient.Post(n.getOpts().NotificationHTTPEndpoint,
			"application/json", bytes.NewBuffer(content))
		if err != nil {
			n.logf(LOG_ERROR, "failed to POST notification - %s", err)
		}
		resp.Body.Close()
	}
}

// 首先开启监听tcp端口，然后把获得的socket给Serve,
// 当然，Serve还需要hander和接口路由等信息，在NewHTTPServer中获取。Serve是对http包的Server封装了一层, 所以至此服务起来了
// handle 使用了Gorilla的压缩代码，对内容执行压缩
// 至于handleAdminActions,就是等待httpServer中的handlers推送消息到chan中，然后handleAdminActions 把相关消息推送到启动服务时注册的notification-http-endpoint中
func (n *NSQAdmin) Main() {
	httpListener, err := net.Listen("tcp", n.getOpts().HTTPAddress)
	if err != nil {
		n.logf(LOG_FATAL, "listen (%s) failed - %s", n.getOpts().HTTPAddress, err)
		os.Exit(1)
	}
	n.Lock()
	n.httpListener = httpListener
	n.Unlock()
	httpServer := NewHTTPServer(&Context{n})
	n.waitGroup.Wrap(func() {
		http_api.Serve(n.httpListener, http_api.CompressHandler(httpServer), "HTTP", n.logf)
	})
	n.waitGroup.Wrap(func() { n.handleAdminActions() })
}

func (n *NSQAdmin) Exit() {
	n.httpListener.Close()
	close(n.notifications)
	n.waitGroup.Wait()
}
