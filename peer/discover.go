package peer

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

// NearbyDevice is a device inviting on the local network: the Android app
// advertises DNS-SD service _scterm._tcp while an invitation is open.
type NearbyDevice struct {
	Name string
	Host string
	Port int
}

const nearbyService = "_scterm._tcp.local."

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// DiscoverNearby asks the local network, for about wait, which devices are
// inviting. An answer only says where a device listens: who it is gets
// settled by the code both users compare, never by the answer.
func DiscoverNearby(ctx context.Context, wait time.Duration) ([]NearbyDevice, error) {
	ifaces := multicastInterfaces()
	if len(ifaces) == 0 {
		return nil, errors.New("no network interface can reach the local network")
	}
	// Queries from a port other than 5353 get unicast answers (RFC 6762 6.7).
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	conns := []*net.UDPConn{conn}
	// Some responders answer by multicast anyway; the port is shared with any
	// local mDNS daemon, and a failure here only loses those answers.
	if group, err := net.ListenMulticastUDP("udp4", nil, mdnsGroup); err == nil {
		gp := ipv4.NewPacketConn(group)
		for _, ifi := range ifaces {
			gp.JoinGroup(&ifi, &net.UDPAddr{IP: mdnsGroup.IP})
		}
		conns = append(conns, group)
	}
	deadline := time.Now().Add(wait)
	for _, c := range conns {
		c.SetReadDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() {
		for _, c := range conns {
			c.Close()
		}
	})
	defer func() {
		stop()
		for _, c := range conns {
			c.Close()
		}
	}()

	found := newNearbyCollector()
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func(c *net.UDPConn) {
			defer wg.Done()
			buf := make([]byte, 9000)
			for {
				n, from, err := c.ReadFromUDP(buf)
				if err != nil {
					return
				}
				found.add(buf[:n], from.IP)
			}
		}(c)
	}

	pc := ipv4.NewPacketConn(conn)
	pc.SetMulticastTTL(255)
	send := func() {
		query, err := nearbyQuery(found.unresolved())
		if err != nil {
			return
		}
		for _, ifi := range ifaces {
			if pc.SetMulticastInterface(&ifi) == nil {
				conn.WriteToUDP(query, mdnsGroup)
			}
		}
	}
	// Three queries, as a lost UDP packet is routine on Wi-Fi.
	for i, at := 0, time.Now(); i < 3 && time.Now().Before(deadline); i++ {
		send()
		select {
		case <-ctx.Done():
		case <-time.After(time.Until(at.Add(time.Duration(i+1) * wait / 3))):
		}
		if ctx.Err() != nil {
			break
		}
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return found.devices(), nil
}

func multicastInterfaces() []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range all {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				out = append(out, ifi)
				break
			}
		}
	}
	return out
}

// nearbyQuery asks for the service, plus the SRV record of any device heard
// of without one.
func nearbyQuery(unresolved []string) ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	names := append([]string{nearbyService}, unresolved...)
	for i, n := range names {
		name, err := dnsmessage.NewName(n)
		if err != nil {
			continue
		}
		typ := dnsmessage.TypeSRV
		if i == 0 {
			typ = dnsmessage.TypePTR
		}
		if err := b.Question(dnsmessage.Question{Name: name, Type: typ, Class: dnsmessage.ClassINET}); err != nil {
			return nil, err
		}
	}
	return b.Finish()
}

type nearbyEntry struct {
	target string
	port   int
	from   net.IP
}

// nearbyCollector assembles devices from answers arriving in any order.
type nearbyCollector struct {
	mu        sync.Mutex
	instances map[string]*nearbyEntry // full instance name, lower case
	names     map[string]string       // lower case -> as advertised
	addrs     map[string]net.IP       // host name, lower case
}

func newNearbyCollector() *nearbyCollector {
	return &nearbyCollector{instances: map[string]*nearbyEntry{}, names: map[string]string{}, addrs: map[string]net.IP{}}
}

func (c *nearbyCollector) instance(name string) *nearbyEntry {
	key := strings.ToLower(name)
	e := c.instances[key]
	if e == nil {
		e = &nearbyEntry{}
		c.instances[key] = e
		c.names[key] = name
	}
	return e
}

// add takes one mDNS packet; anything malformed or unrelated is ignored.
func (c *nearbyCollector) add(packet []byte, from net.IP) {
	var p dnsmessage.Parser
	h, err := p.Start(packet)
	if err != nil || !h.Response {
		return
	}
	if err := p.SkipAllQuestions(); err != nil {
		return
	}
	var records []dnsmessage.Resource
	sections := []func() (dnsmessage.Resource, error){p.Answer, p.Authority, p.Additional}
	for _, next := range sections {
		for {
			r, err := next()
			if err != nil {
				break // ErrSectionDone, or malformed: nothing after it is readable
			}
			records = append(records, r)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range records {
		name := r.Header.Name.String()
		switch body := r.Body.(type) {
		case *dnsmessage.PTRResource:
			if strings.EqualFold(name, nearbyService) {
				c.instance(body.PTR.String())
			}
		case *dnsmessage.SRVResource:
			if isNearbyInstance(name) {
				e := c.instance(name)
				e.target, e.port, e.from = strings.ToLower(body.Target.String()), int(body.Port), from.To4()
			}
		case *dnsmessage.AResource:
			c.addrs[strings.ToLower(name)] = net.IP(body.A[:])
		}
	}
}

func isNearbyInstance(name string) bool {
	return len(name) > len(nearbyService) && strings.EqualFold(name[len(name)-len(nearbyService)-1:], "."+nearbyService)
}

// unresolved lists devices heard of whose port is not known yet.
func (c *nearbyCollector) unresolved() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for key, e := range c.instances {
		if e.port == 0 {
			out = append(out, c.names[key])
		}
	}
	return out
}

func (c *nearbyCollector) devices() []NearbyDevice {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []NearbyDevice
	for key, e := range c.instances {
		if e.port == 0 {
			continue
		}
		host := c.addrs[e.target]
		if host == nil {
			host = e.from // the device answered for itself
		}
		if host == nil {
			continue
		}
		name := c.names[key]
		if isNearbyInstance(name) {
			name = name[:len(name)-len(nearbyService)-1]
		}
		out = append(out, NearbyDevice{Name: name, Host: host.String(), Port: e.port})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}
