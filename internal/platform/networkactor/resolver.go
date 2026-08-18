// Package networkactor derives a privacy-preserving, stable request actor from
// the socket peer and a deliberately configured trusted-proxy chain.
package networkactor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net"
	"net/http"
	"strings"
)

type Resolver struct {
	key     []byte
	trusted []*net.IPNet
}

func New(key []byte, trustedProxyCIDRs []string) (*Resolver, error) {
	if len(key) != 32 {
		return nil, errors.New("network actor hashing requires a 32-byte key")
	}
	resolver := &Resolver{key: append([]byte(nil), key...)}
	for _, raw := range trustedProxyCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			return nil, errors.New("trusted proxy CIDR is invalid")
		}
		resolver.trusted = append(resolver.trusted, network)
	}
	return resolver, nil
}

func (r *Resolver) Actor(request *http.Request) ([32]byte, error) {
	peer, err := socketIP(request.RemoteAddr)
	if err != nil {
		return [32]byte{}, err
	}
	actor := peer
	if r.isTrusted(peer) {
		values := request.Header.Values("X-Forwarded-For")
		chain, valid := forwardedChain(values)
		if len(values) > 0 && !valid {
			return [32]byte{}, errors.New("forwarded request chain is invalid")
		}
		if len(chain) > 0 {
			actor = chain[0]
			for index := len(chain) - 1; index >= 0; index-- {
				actor = chain[index]
				if !r.isTrusted(actor) {
					break
				}
			}
		}
	}
	canonical := actor.To16()
	if canonical == nil {
		return [32]byte{}, errors.New("request peer address is invalid")
	}
	digest := hmac.New(sha256.New, r.key)
	_, _ = digest.Write([]byte("spyglass-network-actor-v1\x00"))
	_, _ = digest.Write(canonical)
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func (r *Resolver) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		actor, err := r.Actor(request)
		if err != nil {
			http.Error(w, "request network identity is unavailable", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, request.WithContext(context.WithValue(request.Context(), actorContextKey{}, actor)))
	})
}

func FromContext(ctx context.Context) ([32]byte, bool) {
	actor, ok := ctx.Value(actorContextKey{}).([32]byte)
	return actor, ok && actor != ([32]byte{})
}

type actorContextKey struct{}

func socketIP(remote string) (net.IP, error) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return nil, errors.New("request peer address is invalid")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, errors.New("request peer address is invalid")
	}
	return ip, nil
}

func forwardedChain(values []string) ([]net.IP, bool) {
	if len(values) == 0 {
		return nil, false
	}
	var result []net.IP
	for _, value := range values {
		for _, raw := range strings.Split(value, ",") {
			ip := net.ParseIP(strings.TrimSpace(raw))
			if ip == nil {
				return nil, false
			}
			result = append(result, ip)
		}
	}
	return result, len(result) > 0
}

func (r *Resolver) isTrusted(ip net.IP) bool {
	for _, network := range r.trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
