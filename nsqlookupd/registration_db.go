package nsqlookupd

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type RegistrationDB struct {
	sync.RWMutex
	registrationMap map[Registration]Producers
}

/*
registrationsmap:

      Registration                                []*Producer(Producers)

                                               _________________________________________________
---------------------------             ------|-----------------------------------             |       ----------------------------------------
| Category | Key | SubKey | -->  [0] -->| *PeerInfo | tombstoned | tombstonedAt |	       |------>| lastUpdate | id | RemoteAddress | ... |
---------------------------             -----------------------------------------                      ----------------------------------------
				 [N] -->| *PeerInfo | tombstoned | tombstonedAt |
				        -----------------------------------------

                                               _________________________________________________
---------------------------             ------|-----------------------------------             |       ----------------------------------------
| Category | Key | SubKey | -->  [0] -->| *PeerInfo | tombstoned | tombstonedAt |	       |------>| lastUpdate | id | RemoteAddress | ... |
---------------------------             -----------------------------------------                      ----------------------------------------
				 [N] -->| *PeerInfo | tombstoned | tombstonedAt |
				        -----------------------------------------
*/

type Registration struct {
	Category string  // 目前发现有client, channel, topic 三种类型
	Key      string  // 目前发现的有：tpoic name
	SubKey   string  // 目前发现的有：channel name 
}
type Registrations []Registration

// producer info
type PeerInfo struct {
	lastUpdate       int64
	id               string  // id 是client.RemoteAddr (IP:Port)
	RemoteAddress    string `json:"remote_address"`
	Hostname         string `json:"hostname"`
	BroadcastAddress string `json:"broadcast_address"`
	TCPPort          int    `json:"tcp_port"`
	HTTPPort         int    `json:"http_port"`
	Version          string `json:"version"`
}

type Producer struct {
	peerInfo     *PeerInfo
	tombstoned   bool
	tombstonedAt time.Time
}

type Producers []*Producer

func (p *Producer) String() string {
	return fmt.Sprintf("%s [%d, %d]", p.peerInfo.BroadcastAddress, p.peerInfo.TCPPort, p.peerInfo.HTTPPort)
}

func (p *Producer) Tombstone() {
	p.tombstoned = true
	p.tombstonedAt = time.Now()
}

func (p *Producer) IsTombstoned(lifetime time.Duration) bool {
	return p.tombstoned && time.Now().Sub(p.tombstonedAt) < lifetime
}

func NewRegistrationDB() *RegistrationDB {
	return &RegistrationDB{
		registrationMap: make(map[Registration]Producers),
	}
}

// add a registration key
func (r *RegistrationDB) AddRegistration(k Registration) {
	r.Lock()
	defer r.Unlock()
	_, ok := r.registrationMap[k]
	if !ok {
		r.registrationMap[k] = Producers{}
	}
}

// add a producer to a registration
// 拿 k 为 client为列：
// 先获取现有的client's producers, RemoteAddr为ID，如果存在该ID， 什么也不做，返回false
// 如果不存在该ID， 则追加该Product 到client 里面，返回true
func (r *RegistrationDB) AddProducer(k Registration, p *Producer) bool {
	r.Lock()
	defer r.Unlock()
	producers := r.registrationMap[k]
	found := false
	for _, producer := range producers {
		if producer.peerInfo.id == p.peerInfo.id {
			found = true
			break
		}
	}
	if found == false {
		r.registrationMap[k] = append(producers, p)
	}
	return !found
}

// remove a producer from a registration
func (r *RegistrationDB) RemoveProducer(k Registration, id string) (bool, int) {
	r.Lock()
	defer r.Unlock()
	producers, ok := r.registrationMap[k]
	if !ok {
		return false, 0
	}
	removed := false
	cleaned := Producers{}
	for _, producer := range producers {
		if producer.peerInfo.id != id {
			cleaned = append(cleaned, producer)
		} else {
			removed = true
		}
	}
	// Note: this leaves keys in the DB even if they have empty lists
	r.registrationMap[k] = cleaned
	return removed, len(cleaned)
}

// remove a Registration and all it's producers
func (r *RegistrationDB) RemoveRegistration(k Registration) {
	r.Lock()
	defer r.Unlock()
	// delete map 中的一个key,就会把key中的指针数组删除没毛病，但是指针指向的对象呢？
	// 如何做到也一起删除呢？ 看来golang的基础没学好
	delete(r.registrationMap, k)
}

func (r *RegistrationDB) needFilter(key string, subkey string) bool {
	return key == "*" || subkey == "*"
}

// 如果key或subkey是×(通配符), 找到所有匹配参数 category, key, subkey的 Registrations
// 如果key和subkey是固定值，则精确匹配并返回 
func (r *RegistrationDB) FindRegistrations(category string, key string, subkey string) Registrations {
	r.RLock()
	defer r.RUnlock()
	if !r.needFilter(key, subkey) {
		// 不需要Filter， 精确匹配
		k := Registration{category, key, subkey}
		if _, ok := r.registrationMap[k]; ok {
			return Registrations{k}
		}
		return Registrations{}
	}
	results := Registrations{}
	for k := range r.registrationMap {
		if !k.IsMatch(category, key, subkey) {
			continue
		}
		results = append(results, k)
	}
	return results
}

// 和上面的是同样的套路，如果没有通配符，就直接返回对应的Producers([]*Producer)
// 如果有通配符，就返回所有匹配的
func (r *RegistrationDB) FindProducers(category string, key string, subkey string) Producers {
	r.RLock()
	defer r.RUnlock()
	if !r.needFilter(key, subkey) {
		k := Registration{category, key, subkey}
		return r.registrationMap[k]
	}

	results := Producers{}
	for k, producers := range r.registrationMap {
		if !k.IsMatch(category, key, subkey) {
			continue
		}
		for _, producer := range producers {
			// 如果已经加入找到过该 producer, 就跳过该producer,
			// 这样是不是表示一个producer可以同时加入多个topic或category？ 还有待观察！
			found := false
			for _, p := range results {
				if producer.peerInfo.id == p.peerInfo.id {
					found = true
				}
			}
			if found == false {
				results = append(results, producer)
			}
		}
	}
	return results
}

func (r *RegistrationDB) LookupRegistrations(id string) Registrations {
	r.RLock()
	defer r.RUnlock()
	results := Registrations{}
	for k, producers := range r.registrationMap {
		for _, p := range producers {
			if p.peerInfo.id == id {
				results = append(results, k)
				break
			}
		}
	}
	return results
}

func (k Registration) IsMatch(category string, key string, subkey string) bool {
	if category != k.Category {
		return false
	}
	if key != "*" && k.Key != key {
		return false
	}
	if subkey != "*" && k.SubKey != subkey {
		return false
	}
	return true
}

func (rr Registrations) Filter(category string, key string, subkey string) Registrations {
	output := Registrations{}
	for _, k := range rr {
		if k.IsMatch(category, key, subkey) {
			output = append(output, k)
		}
	}
	return output
}

func (rr Registrations) Keys() []string {
	keys := make([]string, len(rr))
	for i, k := range rr {
		keys[i] = k.Key
	}
	return keys
}

func (rr Registrations) SubKeys() []string {
	subkeys := make([]string, len(rr))
	for i, k := range rr {
		subkeys[i] = k.SubKey
	}
	return subkeys
}

func (pp Producers) FilterByActive(inactivityTimeout time.Duration, tombstoneLifetime time.Duration) Producers {
	now := time.Now()
	results := Producers{}
	for _, p := range pp {
		cur := time.Unix(0, atomic.LoadInt64(&p.peerInfo.lastUpdate))
		if now.Sub(cur) > inactivityTimeout || p.IsTombstoned(tombstoneLifetime) {
			continue
		}
		results = append(results, p)
	}
	return results
}

func (pp Producers) PeerInfo() []*PeerInfo {
	results := []*PeerInfo{}
	for _, p := range pp {
		results = append(results, p.peerInfo)
	}
	return results
}
