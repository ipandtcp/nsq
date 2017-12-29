package nsqlookupd

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"sync/atomic"

	"github.com/julienschmidt/httprouter"
	"github.com/nsqio/nsq/internal/http_api"
	"github.com/nsqio/nsq/internal/protocol"
	"github.com/nsqio/nsq/internal/version"
)

type httpServer struct {
	ctx    *Context
	router http.Handler
}

func newHTTPServer(ctx *Context) *httpServer {
	// log 是通过nslookupd.logf 生成的一个decorator, decorator 接收 “接口处理函数”APIHandler类型作为参数
	// 它的作用是把接口处理函数包装一边，返回一个包装后的接口处理函数
	log := http_api.Log(ctx.nsqlookupd.logf)

	router := httprouter.New()
	router.HandleMethodNotAllowed = true
	router.PanicHandler = http_api.LogPanicHandler(ctx.nsqlookupd.logf)
	router.NotFound = http_api.LogNotFoundHandler(ctx.nsqlookupd.logf)
	router.MethodNotAllowed = http_api.LogMethodNotAllowedHandler(ctx.nsqlookupd.logf)
	s := &httpServer{
		ctx:    ctx,
		router: router,
	}

	router.Handle("GET", "/ping", http_api.Decorate(s.pingHandler, log, http_api.PlainText))
	router.Handle("GET", "/info", http_api.Decorate(s.doInfo, log, http_api.V1))

	// v1 negotiate
	router.Handle("GET", "/debug", http_api.Decorate(s.doDebug, log, http_api.V1))
	router.Handle("GET", "/lookup", http_api.Decorate(s.doLookup, log, http_api.V1))
	router.Handle("GET", "/topics", http_api.Decorate(s.doTopics, log, http_api.V1))
	router.Handle("GET", "/channels", http_api.Decorate(s.doChannels, log, http_api.V1))
	router.Handle("GET", "/nodes", http_api.Decorate(s.doNodes, log, http_api.V1))

	// only v1
	router.Handle("POST", "/topic/create", http_api.Decorate(s.doCreateTopic, log, http_api.V1))
	router.Handle("POST", "/topic/delete", http_api.Decorate(s.doDeleteTopic, log, http_api.V1))
	router.Handle("POST", "/channel/create", http_api.Decorate(s.doCreateChannel, log, http_api.V1))
	router.Handle("POST", "/channel/delete", http_api.Decorate(s.doDeleteChannel, log, http_api.V1))
	router.Handle("POST", "/topic/tombstone", http_api.Decorate(s.doTombstoneTopicProducer, log, http_api.V1))

	// debug
	router.HandlerFunc("GET", "/debug/pprof", pprof.Index)
	router.HandlerFunc("GET", "/debug/pprof/cmdline", pprof.Cmdline)
	router.HandlerFunc("GET", "/debug/pprof/symbol", pprof.Symbol)
	router.HandlerFunc("POST", "/debug/pprof/symbol", pprof.Symbol)
	router.HandlerFunc("GET", "/debug/pprof/profile", pprof.Profile)
	router.Handler("GET", "/debug/pprof/heap", pprof.Handler("heap"))
	router.Handler("GET", "/debug/pprof/goroutine", pprof.Handler("goroutine"))
	router.Handler("GET", "/debug/pprof/block", pprof.Handler("block"))
	router.Handler("GET", "/debug/pprof/threadcreate", pprof.Handler("threadcreate"))

	return s
}

// 实现该方式是为了实现http.Handler
func (s *httpServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	s.router.ServeHTTP(w, req)
}

// 以下接口都是APIHandler 类型：接口处理函数, 所有的函数都被包装了两层，所有不用担心返回与日志的问题

func (s *httpServer) pingHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	return "OK", nil
}


func (s *httpServer) doInfo(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	return struct {
		Version string `json:"version"`
	}{
		Version: version.Binary,
	}, nil
}

// 搜索该topic所有key, subkey 
func (s *httpServer) doTopics(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	topics := s.ctx.nsqlookupd.DB.FindRegistrations("topic", "*", "").Keys()
	return map[string]interface{}{
		"topics": topics,
	}, nil
}

// 找到特定topicname中的所有channelsname,即 subkey 
func (s *httpServer) doChannels(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	reqParams, err := http_api.NewReqParams(req)
	if err != nil {
		return nil, http_api.Err{400, "INVALID_REQUEST"}
	}

	topicName, err := reqParams.Get("topic")
	if err != nil {
		return nil, http_api.Err{400, "MISSING_ARG_TOPIC"}
	}

	channels := s.ctx.nsqlookupd.DB.FindRegistrations("channel", topicName, "*").SubKeys()
	return map[string]interface{}{
		"channels": channels,
	}, nil
}

// 类型为"topic"时，key是 topic name,subkey 是为空的，有待日后确定 .   --> 已确定，在下面的doCreateTopic 函数
// 先确定是否存在该topicName, 如果存在就获取该topicname的channel分类中所有channelsname和topic分类中的所有Products
// 然后筛选出Active的Producter
func (s *httpServer) doLookup(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	reqParams, err := http_api.NewReqParams(req)
	if err != nil {
		return nil, http_api.Err{400, "INVALID_REQUEST"}
	}

	topicName, err := reqParams.Get("topic")
	if err != nil {
		return nil, http_api.Err{400, "MISSING_ARG_TOPIC"}
	}

	registration := s.ctx.nsqlookupd.DB.FindRegistrations("topic", topicName, "")
	if len(registration) == 0 {
		return nil, http_api.Err{404, "TOPIC_NOT_FOUND"}
	}

	channels := s.ctx.nsqlookupd.DB.FindRegistrations("channel", topicName, "*").SubKeys()
	producers := s.ctx.nsqlookupd.DB.FindProducers("topic", topicName, "")
	producers = producers.FilterByActive(s.ctx.nsqlookupd.opts.InactiveProducerTimeout,
		s.ctx.nsqlookupd.opts.TombstoneLifetime)
	return map[string]interface{}{
		"channels":  channels,
		"producers": producers.PeerInfo(),
	}, nil
}

// 获取topicname ,并检查是否是合法的topicname, 如果是，就加入到topic分类中
func (s *httpServer) doCreateTopic(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	reqParams, err := http_api.NewReqParams(req)
	if err != nil {
		return nil, http_api.Err{400, "INVALID_REQUEST"}
	}

	topicName, err := reqParams.Get("topic")
	if err != nil {
		return nil, http_api.Err{400, "MISSING_ARG_TOPIC"}
	}

	if !protocol.IsValidTopicName(topicName) {
		return nil, http_api.Err{400, "INVALID_ARG_TOPIC"}
	}

	s.ctx.nsqlookupd.logf(LOG_INFO, "DB: adding topic(%s)", topicName)
	key := Registration{"topic", topicName, ""}
	s.ctx.nsqlookupd.DB.AddRegistration(key)

	return nil, nil
}


// 删除topic 时，把类别channel 和 topic 中的的都删除，包括Registrations 中的Producer 
func (s *httpServer) doDeleteTopic(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	reqParams, err := http_api.NewReqParams(req)
	if err != nil {
		return nil, http_api.Err{400, "INVALID_REQUEST"}
	}

	topicName, err := reqParams.Get("topic")
	if err != nil {
		return nil, http_api.Err{400, "MISSING_ARG_TOPIC"}
	}

	registrations := s.ctx.nsqlookupd.DB.FindRegistrations("channel", topicName, "*")
	for _, registration := range registrations {
		s.ctx.nsqlookupd.logf(LOG_INFO, "DB: removing channel(%s) from topic(%s)", registration.SubKey, topicName)
		s.ctx.nsqlookupd.DB.RemoveRegistration(registration)
	}

	registrations = s.ctx.nsqlookupd.DB.FindRegistrations("topic", topicName, "")
	for _, registration := range registrations {
		s.ctx.nsqlookupd.logf(LOG_INFO, "DB: removing topic(%s)", topicName)
		s.ctx.nsqlookupd.DB.RemoveRegistration(registration)
	}

	return nil, nil
}

// 指定topic和node, Tombstone it 
func (s *httpServer) doTombstoneTopicProducer(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	reqParams, err := http_api.NewReqParams(req)
	if err != nil {
		return nil, http_api.Err{400, "INVALID_REQUEST"}
	}

	topicName, err := reqParams.Get("topic")
	if err != nil {
		return nil, http_api.Err{400, "MISSING_ARG_TOPIC"}
	}

	node, err := reqParams.Get("node")
	if err != nil {
		return nil, http_api.Err{400, "MISSING_ARG_NODE"}
	}

	s.ctx.nsqlookupd.logf(LOG_INFO, "DB: setting tombstone for producer@%s of topic(%s)", node, topicName)
	producers := s.ctx.nsqlookupd.DB.FindProducers("topic", topicName, "")
	for _, p := range producers {
		thisNode := fmt.Sprintf("%s:%d", p.peerInfo.BroadcastAddress, p.peerInfo.HTTPPort)
		if thisNode == node {
			p.Tombstone()
		}
	}

	return nil, nil
}

// 添加一个Channel， 即要添加到channel分类，也要添加到topic分类
func (s *httpServer) doCreateChannel(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	reqParams, err := http_api.NewReqParams(req)
	if err != nil {
		return nil, http_api.Err{400, "INVALID_REQUEST"}
	}

	topicName, channelName, err := http_api.GetTopicChannelArgs(reqParams)
	if err != nil {
		return nil, http_api.Err{400, err.Error()}
	}

	s.ctx.nsqlookupd.logf(LOG_INFO, "DB: adding channel(%s) in topic(%s)", channelName, topicName)
	key := Registration{"channel", topicName, channelName}
	s.ctx.nsqlookupd.DB.AddRegistration(key)

	s.ctx.nsqlookupd.logf(LOG_INFO, "DB: adding topic(%s)", topicName)
	key = Registration{"topic", topicName, ""}
	s.ctx.nsqlookupd.DB.AddRegistration(key)

	return nil, nil
}

// 删除channel分类中的topicName & channelName,。
// 这里有个疑惑，创建的时候同时在channel和topic分类中创建了Registration, 但是删除的时候只删除了channel中的，不太理解
func (s *httpServer) doDeleteChannel(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	reqParams, err := http_api.NewReqParams(req)
	if err != nil {
		return nil, http_api.Err{400, "INVALID_REQUEST"}
	}

	topicName, channelName, err := http_api.GetTopicChannelArgs(reqParams)
	if err != nil {
		return nil, http_api.Err{400, err.Error()}
	}

	registrations := s.ctx.nsqlookupd.DB.FindRegistrations("channel", topicName, channelName)
	if len(registrations) == 0 {
		return nil, http_api.Err{404, "CHANNEL_NOT_FOUND"}
	}

	s.ctx.nsqlookupd.logf(LOG_INFO, "DB: removing channel(%s) from topic(%s)", channelName, topicName)
	for _, registration := range registrations {
		s.ctx.nsqlookupd.DB.RemoveRegistration(registration)
	}

	return nil, nil
}

type node struct {
	RemoteAddress    string   `json:"remote_address"`
	Hostname         string   `json:"hostname"`
	BroadcastAddress string   `json:"broadcast_address"`
	TCPPort          int      `json:"tcp_port"`
	HTTPPort         int      `json:"http_port"`
	Version          string   `json:"version"`
	Tombstones       []bool   `json:"tombstones"`
	Topics           []string `json:"topics"`
}


// 找到所有client类型中的Producers,
// 再找到topic类型中的所有key,再根据这些key,找到所有的Producers,然后做一些查询，最后返回
// 下面有一些我自作聪明的优化，由于对整个项目还不是很了解，不知道会不会产生其他问题，优化的也并不好，急着敢末班车，先闪了
func (s *httpServer) doNodes(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	// dont filter out tombstoned nodes
	producers := s.ctx.nsqlookupd.DB.FindProducers("client", "", "").FilterByActive(
		s.ctx.nsqlookupd.opts.InactiveProducerTimeout, 0)
	nodes := make([]*node, len(producers))

	topics     := s.ctx.nsqlookupd.DB.LookupRegistrations(p.peerInfo.id).Filter("topic", "*", "").Keys()
	tombstones := make([]bool, len(topics))
	topicProducers := Producers{}
	for j, t := range topics {
		topicProducers = append(topicProduers, s.ctx.nsqlookupd.DB.FindProducers("topic", t, "")...)
	}

	for i, p := range producers {
		//topics := s.ctx.nsqlookupd.DB.LookupRegistrations(p.peerInfo.id).Filter("topic", "*", "").Keys()

		// for each topic find the producer that matches this peer
		// to add tombstone information
		//tombstones := make([]bool, len(topics))
		for j, t := range topics {
			//topicProducers := s.ctx.nsqlookupd.DB.FindProducers("topic", t, "")
			for _, tp := range topicProducers {
				if tp.peerInfo == p.peerInfo {
					tombstones[j] = tp.IsTombstoned(s.ctx.nsqlookupd.opts.TombstoneLifetime)
				}
			}
		}

		nodes[i] = &node{
			RemoteAddress:    p.peerInfo.RemoteAddress,
			Hostname:         p.peerInfo.Hostname,
			BroadcastAddress: p.peerInfo.BroadcastAddress,
			TCPPort:          p.peerInfo.TCPPort,
			HTTPPort:         p.peerInfo.HTTPPort,
			Version:          p.peerInfo.Version,
			Tombstones:       tombstones,
			Topics:           topics,
		}
	}

	return map[string]interface{}{
		"producers": nodes,
	}, nil
}


// 返回DB中所有内容，一般用于调试
func (s *httpServer) doDebug(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (interface{}, error) {
	s.ctx.nsqlookupd.DB.RLock()
	defer s.ctx.nsqlookupd.DB.RUnlock()

	data := make(map[string][]map[string]interface{})
	for r, producers := range s.ctx.nsqlookupd.DB.registrationMap {
		key := r.Category + ":" + r.Key + ":" + r.SubKey
		for _, p := range producers {
			m := map[string]interface{}{
				"id":                p.peerInfo.id,
				"hostname":          p.peerInfo.Hostname,
				"broadcast_address": p.peerInfo.BroadcastAddress,
				"tcp_port":          p.peerInfo.TCPPort,
				"http_port":         p.peerInfo.HTTPPort,
				"version":           p.peerInfo.Version,
				"last_update":       atomic.LoadInt64(&p.peerInfo.lastUpdate),
				"tombstoned":        p.tombstoned,
				"tombstoned_at":     p.tombstonedAt.UnixNano(),
			}
			data[key] = append(data[key], m)
		}
	}

	return data, nil
}
