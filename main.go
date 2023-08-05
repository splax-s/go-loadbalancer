package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

type Server struct {
	URL     *url.URL
	Alive   bool
	RWMutex sync.RWMutex
}

func (s *Server) SetAlive(alive bool) {
	s.RWMutex.Lock()
	s.Alive = alive
	s.RWMutex.Unlock()
}

func (s *Server) IsAlive() (alive bool) {
	s.RWMutex.RLock()
	alive = s.Alive
	s.RWMutex.RUnlock()
	return
}

func isServerAlive(u *url.URL) bool {
	timeout := time.Second
	conn, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		fmt.Println("Site unreachable, error: ", err)
		return false
	}
	_ = conn.Close()
	return true
}

type LoadBalancer struct {
	Servers []*Server
	Current uint64
}

func (b *LoadBalancer) HealthCheck() {
	t := time.NewTicker(time.Minute)
	for {
		select {
		case <-t.C:
			for _, s := range b.Servers {
				status := "up"
				alive := isServerAlive(s.URL)
				s.SetAlive(alive)
				if !alive {
					status = "down"
				}
				fmt.Printf("%s [%s]\n", s.URL, status)
			}
		}
	}
}

func (b *LoadBalancer) NextIndex() int {
	return int(atomic.AddUint64(&b.Current, uint64(1)) % uint64(len(b.Servers)))
}

func (b *LoadBalancer) GetNextPeer() *Server {
	next := b.NextIndex()
	l := len(b.Servers) + next
	for i := next; i < l; i++ {
		idx := i % len(b.Servers)
		if b.Servers[idx].IsAlive() {
			atomic.StoreUint64(&b.Current, uint64(idx))
			return b.Servers[idx]
		}
	}
	return nil
}

func main() {
	urls := []string{
		"http://localhost:3000",
		"http://localhost:8081",
		"http://localhost:19000",
	}

	servers := make([]*Server, len(urls))
	for i, u := range urls {
		url, _ := url.Parse(u)
		servers[i] = &Server{
			URL:   url,
			Alive: true,
		}
	}
	b := &LoadBalancer{
		Servers: servers,
	}

	go b.HealthCheck()

	r := gin.Default()

	r.GET("/balancer", func(c *gin.Context) {
		peer := b.GetNextPeer()

		if peer != nil {
			proxy := httputil.NewSingleHostReverseProxy(peer.URL)
			proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
				log.Println("Error:", err.Error())
				rw.WriteHeader(http.StatusBadGateway)
			}
			proxy.ServeHTTP(c.Writer, c.Request)
		} else {
			c.String(http.StatusServiceUnavailable, "No servers available.")
		}
	})

	r.Run(":8083")
}
