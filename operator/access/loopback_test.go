package access

import "testing"

func TestCheckCallerLoopback(t *testing.T) {
	ok := []Caller{
		{ClientIP: "127.0.0.1"},
		{ClientIP: "::1"},
		{ClientIP: "localhost"},
		{HasHTTP: true, RemoteAddr: "127.0.0.1:43123", ClientIP: "127.0.0.1"},
		{HasHTTP: true, RemoteAddr: "[::1]:9"},
		{HasHTTP: true, RemoteAddr: "127.0.0.1:9", ForwardedFor: "127.0.0.1, ::1", RealIP: "127.0.0.1"},
	}
	for _, c := range ok {
		if err := CheckCaller(c); err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	bad := []Caller{
		{},
		{ClientIP: "172.30.0.2"},
		{ClientIP: "10.0.0.8"},
		{ClientIP: "192.168.1.9"},
		{HasHTTP: true, RemoteAddr: "172.30.0.2:54321", ClientIP: "127.0.0.1"},
		{HasHTTP: true, RemoteAddr: "127.0.0.1:9", ClientIP: "127.0.0.1", ForwardedFor: "172.30.0.2"},
		{HasHTTP: true, RemoteAddr: "127.0.0.1:9", ForwardedFor: "127.0.0.1, 10.1.1.1"},
		{HasHTTP: true, RemoteAddr: "127.0.0.1:9", RealIP: "192.168.0.4"},
		{HasHTTP: true, RemoteAddr: ""},
	}
	for _, c := range bad {
		err := CheckCaller(c)
		if err == nil {
			t.Fatalf("allowed %+v", c)
		}
	}
}
