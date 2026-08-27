package engine

import ("io"; "net"; "time")
// nopConn lets Control.send work without a real socket in tests.
type nopConn struct{}

func (n *nopConn) Write(p []byte) (int, error) { return len(p), nil }
func (n *nopConn) SetWriteDeadline(t time.Time) error { return nil }
func (n *nopConn) SetReadDeadline(t time.Time) error { return nil }
func (n *nopConn) Read(p []byte) (int, error) { return 0, io.EOF }
func (n *nopConn) Close() error { return nil }
func (n *nopConn) SetDeadline(t time.Time) error { return nil }
func (n *nopConn) LocalAddr() net.Addr { return nil }
func (n *nopConn) RemoteAddr() net.Addr { return nil }
