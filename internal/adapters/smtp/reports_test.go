package smtp

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	app "github.com/tinfoyle/spyglass-engine/internal/application/scheduledreports"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportSMTPAcceptanceAmbiguityAndEscaping(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			fixture := httptest.NewTLSServer(nil)
			cert := fixture.TLS.Certificates[0]
			fixture.Close()
			listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
				t.Fatal(err)
			}
			received := make(chan string, 1)
			go func() {
				c, err := listener.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				wire := textproto.NewConn(c)
				wire.PrintfLine("220 fixture")
				for {
					line, err := wire.ReadLine()
					if err != nil {
						return
					}
					if line == "DATA" {
						wire.PrintfLine("354 send data")
						body, err := wire.ReadDotBytes()
						if err != nil {
							return
						}
						received <- string(body)
						if accepted {
							wire.PrintfLine("250 accepted")
						}
						return
					}
					wire.PrintfLine("250 fixture")
				}
			}()
			sender, err := New(Config{Address: listener.Addr().String(), ServerName: "127.0.0.1", FromAddress: "reports@example.com", AppOrigin: "https://app.example", RootCAFile: ca, Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			id := "10000000-0000-4000-8000-000000000001"
			message := app.Message{ID: id, To: "owner@example.com", Subject: "Mulch report", Body: "<script>price</script>", ConversationID: "20000000-0000-4000-8000-000000000001", BoardroomID: "30000000-0000-4000-8000-000000000001", ScheduleID: "40000000-0000-4000-8000-000000000001"}
			err = sender.SendReport(context.Background(), message)
			if accepted && err != nil || !accepted && !errors.Is(err, app.ErrUnknown) {
				t.Fatalf("accepted=%v err=%v", accepted, err)
			}
			select {
			case body := <-received:
				for _, part := range []string{"Message-ID: <spyglass-report-" + id + "@example.com>", "&lt;script&gt;price&lt;/script&gt;", "/app/agents/boardrooms/30000000-0000-4000-8000-000000000001/conversations/20000000-0000-4000-8000-000000000001"} {
					if !strings.Contains(body, part) {
						t.Fatalf("missing MIME content %s", part)
					}
				}
			case <-time.After(time.Second):
				t.Fatal("message not received")
			}
		})
	}
}
