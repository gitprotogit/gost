// +build !windows

package gost

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	
	"github.com/go-log/log"
	"time"
	"sync"
)

var IpProxyMap sync.Map

var ProxyChainMap sync.Map

type tcpRedirectSPHandler struct {
	options *HandlerOptions
}

// TCPRedirectHandler creates a server Handler for TCP redirect server.
func TcpRedirectSPHandler(opts ...HandlerOption) Handler {
	h := &tcpRedirectHandler{
		options: &HandlerOptions{
			Chain: new(Chain),
		},
	}
	for _, opt := range opts {
		opt(h.options)
	}
	return h
}

func (h *tcpRedirectSPHandler) Handle(c net.Conn) {
	conn, ok := c.(*net.TCPConn)
	if !ok {
		log.Log("[red-tcp] not a TCP connection")
	}
	
	srcAddr := conn.RemoteAddr()
	
	fmt.Printf("[red-tcpssssssssssssss] %s -> %s", srcAddr.Network(), srcAddr.String())
	
	dstAddr, conn, err := h.getOriginalDstAddr(conn)
	if err != nil {
		fmt.Printf("[red-tcp] %s -> %s : %s", srcAddr, dstAddr, err)
		return
	}
	defer conn.Close()
	
	fmt.Printf("[red-tcp] %s -> %s", srcAddr, dstAddr)
	
	var proxy interface{}
	if proxy, ok = IpProxyMap.Load(srcAddr.String()); !ok {
		fmt.Printf("[red-tcp] %s -> %s : %s", srcAddr, dstAddr, "IpProxyMap not exist err")
		return
	}
	
	var chainInterface interface{}
	if chainInterface, ok = ProxyChainMap.Load(srcAddr.String()); !ok {
		chain := NewChain()
		chain.Retries = 3
		ngroup := NewNodeGroup()
		ngroup.ID = 1
		node, err := ParseNode(proxy.(string))
		if err != nil {
			return
		}
		var tr Transporter
		var connector = SOCKS5Connector(node.User)
		timeout := node.GetInt("timeout")
		handshakeOptions := []HandshakeOption{
			AddrHandshakeOption(node.Addr),
			HostHandshakeOption(node.Host),
			IntervalHandshakeOption(time.Duration(node.GetInt("ping")) * time.Second),
			TimeoutHandshakeOption(time.Duration(timeout) * time.Second),
			RetryHandshakeOption(node.GetInt("retry")),
		}
		node.Client = &Client{
			Connector:   connector,
			Transporter: tr,
		}
		node.HandshakeOptions = handshakeOptions
		ngroup.AddNode(node)
		chain.AddNodeGroup(ngroup)
		ProxyChainMap.Store(srcAddr.String(), chain)
		chainInterface = chain
	}
	cc, err := chainInterface.(*Chain).Dial(dstAddr.String())
	if err != nil {
		fmt.Printf("[red-tcp] %s -> %s : %s", srcAddr, dstAddr, err)
		return
	}
	defer cc.Close()
	
	fmt.Printf("[red-tcp] %s <-> %s", srcAddr, dstAddr)
	transport(conn, cc)
	fmt.Printf("[red-tcp] %s >-< %s", srcAddr, dstAddr)
}

func (h *tcpRedirectSPHandler) getOriginalDstAddr(conn *net.TCPConn) (addr net.Addr, c *net.TCPConn, err error) {
	defer conn.Close()
	
	fc, err := conn.File()
	if err != nil {
		return
	}
	defer fc.Close()
	
	mreq, err := syscall.GetsockoptIPv6Mreq(int(fc.Fd()), syscall.IPPROTO_IP, 80)
	if err != nil {
		return
	}
	
	// only ipv4 support
	ip := net.IPv4(mreq.Multiaddr[4], mreq.Multiaddr[5], mreq.Multiaddr[6], mreq.Multiaddr[7])
	port := uint16(mreq.Multiaddr[2])<<8 + uint16(mreq.Multiaddr[3])
	addr, err = net.ResolveTCPAddr("tcp4", fmt.Sprintf("%s:%d", ip.String(), port))
	if err != nil {
		return
	}
	
	cc, err := net.FileConn(fc)
	if err != nil {
		return
	}
	
	c, ok := cc.(*net.TCPConn)
	if !ok {
		err = errors.New("not a TCP connection")
	}
	return
}
