// Package routecontext signs the short-lived, request-bound authority passed
// from Spyglass's global app router to one cell API.
package routecontext

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	TokenType             = "SPYGLASS-ROUTE"
	Algorithm             = "HS256"
	Version               = 1
	MaxTokenBytes         = 32 * 1024
	MaxTargetBytes        = 4096
	MinimumKeyBytes       = 32
	DefaultLifetime       = 20 * time.Second
	MaximumLifetime       = 30 * time.Second
	DefaultClockSkew      = 2 * time.Second
	RotationCanaryActorID = "route-rotation-canary"
)

var (
	ErrInvalid      = errors.New("route context is invalid")
	ErrSignature    = errors.New("route context signature is invalid")
	ErrExpired      = errors.New("route context has expired")
	ErrRequest      = errors.New("route context does not bind this request")
	ErrUnknownKey   = errors.New("route context signing key is unknown")
	ErrReplay       = errors.New("route context was already consumed")
	ErrPlacement    = errors.New("route context placement is stale")
	ErrUnavailable  = errors.New("route context account is unavailable")
	ErrReceiptStore = errors.New("route context receipt store is unavailable")
	machineCode     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	cellCode        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Clock interface{ Now() time.Time }

type Binding struct {
	Method        string `json:"method"`
	Target        string `json:"target"`
	BodySHA256    string `json:"body_sha256"`
	HeadersSHA256 string `json:"headers_sha256,omitempty"`
}

func Bind(method, target string, body []byte) (Binding, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" || strings.ContainsAny(method, " \t\r\n") || target == "" || len(target) > MaxTargetBytes || target[0] != '/' || strings.ContainsAny(target, "\r\n#") || strings.Contains(target, "://") {
		return Binding{}, ErrInvalid
	}
	digest := sha256.Sum256(body)
	return Binding{Method: method, Target: target, BodySHA256: hex.EncodeToString(digest[:])}, nil
}

// BindRequest also authenticates the small set of request headers that can
// change command semantics. Hop-by-hop and presentation headers are excluded.
func BindRequest(request *http.Request, body []byte) (Binding, error) {
	if request == nil {
		return Binding{}, ErrInvalid
	}
	binding, err := Bind(request.Method, Target(request), body)
	if err != nil {
		return Binding{}, err
	}
	values := []string{
		strings.TrimSpace(request.Header.Get("Content-Type")),
		strings.TrimSpace(request.Header.Get("Idempotency-Key")),
		strings.TrimSpace(request.Header.Get("If-Match")),
	}
	if values[0] == "" && values[1] == "" && values[2] == "" {
		return binding, nil
	}
	digest := sha256.Sum256([]byte("content-type:" + values[0] + "\nidempotency-key:" + values[1] + "\nif-match:" + values[2] + "\n"))
	binding.HeadersSHA256 = hex.EncodeToString(digest[:])
	return binding, nil
}

type LimitPolicy struct {
	Kind                  string `json:"kind"`
	Combine               string `json:"combine"`
	ReservationTTLSeconds int64  `json:"reservation_ttl_seconds,omitempty"`
}

type PackageAccess struct {
	Code          string                 `json:"code"`
	Version       uint64                 `json:"version"`
	Mode          string                 `json:"mode"`
	Limits        map[string]int64       `json:"limits,omitempty"`
	LimitPolicies map[string]LimitPolicy `json:"limit_policies,omitempty"`
}

type Authority struct {
	RequestID           string         `json:"request_id"`
	OperationID         string         `json:"operation_id,omitempty"`
	AccountID           ids.AccountID  `json:"account_id"`
	ActorKind           string         `json:"actor_kind"`
	ActorID             string         `json:"actor_id"`
	Role                string         `json:"role,omitempty"`
	CellID              ids.CellID     `json:"cell_id"`
	PlacementGeneration uint64         `json:"placement_generation"`
	EntitlementVersion  uint64         `json:"entitlement_version"`
	PackageAccess       *PackageAccess `json:"package_access,omitempty"`
}

type Claims struct {
	Issuer    string    `json:"iss"`
	Audience  string    `json:"aud"`
	IssuedAt  int64     `json:"iat"`
	ExpiresAt int64     `json:"exp"`
	Authority Authority `json:"authority"`
	Binding   Binding   `json:"binding"`
	KeyID     string    `json:"-"`
}

type header struct {
	Version int    `json:"v"`
	KeyID   string `json:"kid"`
	Type    string `json:"typ"`
	Alg     string `json:"alg"`
}

type Signer struct {
	issuer   string
	keyID    string
	key      []byte
	lifetime time.Duration
	clock    Clock
}

func NewSigner(issuer, keyID string, key []byte, lifetime time.Duration, clock Clock) (*Signer, error) {
	issuer, keyID = strings.TrimSpace(issuer), strings.TrimSpace(keyID)
	if issuer == "" || len(issuer) > 100 || keyID == "" || len(keyID) > 100 || len(key) < MinimumKeyBytes || lifetime <= 0 || lifetime > MaximumLifetime || clock == nil {
		return nil, ErrInvalid
	}
	return &Signer{issuer: issuer, keyID: keyID, key: append([]byte(nil), key...), lifetime: lifetime, clock: clock}, nil
}

func (s *Signer) Issue(audience string, authority Authority, binding Binding) (string, error) {
	audience = strings.TrimSpace(audience)
	if audience == "" || len(audience) > 200 || audience != Audience(authority.CellID) || !validAuthority(authority) || !validBinding(binding) {
		return "", ErrInvalid
	}
	now := s.clock.Now().UTC().Truncate(time.Second)
	claims := Claims{Issuer: s.issuer, Audience: audience, IssuedAt: now.Unix(), ExpiresAt: now.Add(s.lifetime).Unix(), Authority: authority, Binding: binding}
	headerJSON, err := json.Marshal(header{Version: Version, KeyID: s.keyID, Type: TokenType, Alg: Algorithm})
	if err != nil {
		return "", ErrInvalid
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", ErrInvalid
	}
	headerPart := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsPart := base64.RawURLEncoding.EncodeToString(claimsJSON)
	input := headerPart + "." + claimsPart
	signature := sign(s.key, input)
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

type Verifier struct {
	audience    string
	issuer      string
	keys        map[string][]byte
	maxLifetime time.Duration
	clockSkew   time.Duration
	clock       Clock
}

func NewVerifier(issuer, audience string, keys map[string][]byte, maxLifetime, clockSkew time.Duration, clock Clock) (*Verifier, error) {
	issuer, audience = strings.TrimSpace(issuer), strings.TrimSpace(audience)
	if issuer == "" || audience == "" || len(keys) == 0 || maxLifetime <= 0 || maxLifetime > MaximumLifetime || clockSkew < 0 || clockSkew > 5*time.Second || clock == nil {
		return nil, ErrInvalid
	}
	copyKeys := make(map[string][]byte, len(keys))
	for keyID, key := range keys {
		keyID = strings.TrimSpace(keyID)
		if keyID == "" || len(keyID) > 100 || len(key) < MinimumKeyBytes {
			return nil, ErrInvalid
		}
		copyKeys[keyID] = append([]byte(nil), key...)
	}
	return &Verifier{issuer: issuer, audience: audience, keys: copyKeys, maxLifetime: maxLifetime, clockSkew: clockSkew, clock: clock}, nil
}

func (v *Verifier) Verify(token string, binding Binding) (Claims, error) {
	if token == "" || len(token) > MaxTokenBytes || !validBinding(binding) {
		return Claims{}, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalid
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var tokenHeader header
	if err := decodeStrict(headerBytes, &tokenHeader); err != nil || tokenHeader.Version != Version || tokenHeader.Type != TokenType || tokenHeader.Alg != Algorithm {
		return Claims{}, ErrInvalid
	}
	key, exists := v.keys[tokenHeader.KeyID]
	if !exists {
		return Claims{}, ErrUnknownKey
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size || !hmac.Equal(signature, sign(key, parts[0]+"."+parts[1])) {
		return Claims{}, ErrSignature
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var claims Claims
	if err := decodeStrict(claimsBytes, &claims); err != nil || claims.Issuer != v.issuer || claims.Audience != v.audience || claims.Audience != Audience(claims.Authority.CellID) || !validAuthority(claims.Authority) || !validBinding(claims.Binding) {
		return Claims{}, ErrInvalid
	}
	issuedAt, expiresAt := time.Unix(claims.IssuedAt, 0), time.Unix(claims.ExpiresAt, 0)
	now := v.clock.Now().UTC()
	if !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) > v.maxLifetime || issuedAt.After(now.Add(v.clockSkew)) {
		return Claims{}, ErrInvalid
	}
	if !expiresAt.After(now) {
		return Claims{}, ErrExpired
	}
	if claims.Binding != binding {
		return Claims{}, ErrRequest
	}
	claims.KeyID = tokenHeader.KeyID
	return claims, nil
}

type ReceiptStore interface {
	Consume(context.Context, Claims, time.Time) error
}

type Acceptor struct {
	verifier *Verifier
	receipts ReceiptStore
	clock    Clock
}

func NewAcceptor(verifier *Verifier, receipts ReceiptStore, clock Clock) (*Acceptor, error) {
	if verifier == nil || receipts == nil || clock == nil {
		return nil, ErrInvalid
	}
	return &Acceptor{verifier: verifier, receipts: receipts, clock: clock}, nil
}

func (a *Acceptor) Accept(ctx context.Context, token string, binding Binding) (Claims, error) {
	claims, err := a.verifier.Verify(token, binding)
	if err != nil {
		return Claims{}, err
	}
	if err := a.receipts.Consume(ctx, claims, a.clock.Now().UTC()); err != nil {
		return Claims{}, err
	}
	return claims, nil
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func validAuthority(authority Authority) bool {
	if ids.Validate(authority.RequestID) != nil || ids.Validate(string(authority.AccountID)) != nil || !cellCode.MatchString(string(authority.CellID)) || authority.PlacementGeneration == 0 || authority.EntitlementVersion == 0 {
		return false
	}
	if authority.OperationID != "" && ids.Validate(authority.OperationID) != nil {
		return false
	}
	switch authority.ActorKind {
	case "user":
		if ids.Validate(authority.ActorID) != nil || !validRole(authority.Role) {
			return false
		}
	case "workload":
		if strings.TrimSpace(authority.ActorID) == "" || len(authority.ActorID) > 200 || authority.Role != "" {
			return false
		}
	default:
		return false
	}
	return authority.PackageAccess == nil || validPackageAccess(*authority.PackageAccess)
}

func validPackageAccess(access PackageAccess) bool {
	if !machineCode.MatchString(access.Code) || access.Version == 0 || (access.Mode != "enabled" && access.Mode != "read_only" && access.Mode != "suspended") || len(access.Limits) > 100 || len(access.LimitPolicies) > 100 {
		return false
	}
	for code, value := range access.Limits {
		if !machineCode.MatchString(code) || value < 0 {
			return false
		}
	}
	for code, policy := range access.LimitPolicies {
		if !machineCode.MatchString(code) || policy.Kind != "capacity" || (policy.Combine != "replace" && policy.Combine != "add" && policy.Combine != "maximum" && policy.Combine != "minimum") || policy.ReservationTTLSeconds < 0 || policy.ReservationTTLSeconds > int64((30*24*time.Hour)/time.Second) {
			return false
		}
	}
	return true
}

func validRole(role string) bool {
	return role == "owner" || role == "administrator" || role == "billing_admin" || role == "member" || role == "viewer"
}

func validBinding(binding Binding) bool {
	if _, err := Bind(binding.Method, binding.Target, nil); err != nil {
		return false
	}
	if len(binding.BodySHA256) != sha256.Size*2 {
		return false
	}
	if _, err := hex.DecodeString(binding.BodySHA256); err != nil {
		return false
	}
	if binding.HeadersSHA256 != "" {
		if len(binding.HeadersSHA256) != sha256.Size*2 {
			return false
		}
		if _, err := hex.DecodeString(binding.HeadersSHA256); err != nil {
			return false
		}
	}
	return binding.Method == strings.ToUpper(binding.Method)
}

func sign(key []byte, input string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(input))
	return mac.Sum(nil)
}

type RequestClaimsKey struct{}
type RequestProofKey struct{}

type Proof struct {
	Token   string
	Binding Binding
}

func WithClaims(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, RequestClaimsKey{}, claims)
}
func FromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(RequestClaimsKey{}).(Claims)
	return claims, ok
}

func WithProof(ctx context.Context, proof Proof) context.Context {
	return context.WithValue(ctx, RequestProofKey{}, proof)
}

func ProofFromContext(ctx context.Context) (Proof, bool) {
	proof, ok := ctx.Value(RequestProofKey{}).(Proof)
	return proof, ok && proof.Token != "" && validBinding(proof.Binding)
}

func Target(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	if r.URL.RawQuery == "" {
		return r.URL.EscapedPath()
	}
	return r.URL.EscapedPath() + "?" + r.URL.RawQuery
}

func Audience(cellID ids.CellID) string  { return fmt.Sprintf("spyglass:cell:%s", cellID) }
func ValidCellID(cellID ids.CellID) bool { return cellCode.MatchString(string(cellID)) }
