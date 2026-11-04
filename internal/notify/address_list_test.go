package notify

import (
	"net"
	"net/mail"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSplitAddrsQuotedRecipients(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []string
	}{
		{"quoted comma", `"Doe, Jane" <jane@example.test>, bob@example.test`, []string{"jane@example.test", "bob@example.test"}},
		{"quoted semicolon with legacy separators", `"Team; Alerts" <alerts@example.test>; bob@example.test`, []string{"alerts@example.test", "bob@example.test"}},
		{"escaped quote", `"Doe, \"Jane\"" <jane@example.test>; bob@example.test`, []string{"jane@example.test", "bob@example.test"}},
		{"comment separators", `jane@example.test (Doe, Jane; team); bob@example.test`, []string{"jane@example.test", "bob@example.test"}},
		{"nested comments", `jane@example.test (team (east; west), alerts); bob@example.test`, []string{"jane@example.test", "bob@example.test"}},
		{"group and legacy separator", `Team: jane@example.test, bob@example.test;; carol@example.test`, []string{"jane@example.test", "bob@example.test", "carol@example.test"}},
		{"group", `Team: jane@example.test, bob@example.test;`, []string{"jane@example.test", "bob@example.test"}},
		{"empty entries", `; jane@example.test, , bob@example.test;`, []string{"jane@example.test", "bob@example.test"}},
		{"quoted local part", `"jane,alerts"@example.test; bob@example.test`, []string{`jane,alerts@example.test`, "bob@example.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitAddrs(tc.input); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("splitAddrs(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func splitAddrs(to string) []string {
	addresses, err := parseRecipients(to)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addresses {
		out = append(out, a.Address)
	}
	return out
}

func TestParseRecipientsRejectsInvalidList(t *testing.T) {
	for _, input := range []string{"", ";,;", "Team:;", "valid@example.test, invalid", "valid@example.test; bad@@example.test", "valid@example.test\r\nBcc: other@example.test", `"unterminated <a@example.test>`, `a@example.test (unclosed`, `a@example.test <broken>`} {
		t.Run(input, func(t *testing.T) {
			if addresses, err := parseRecipients(input); err == nil || addresses != nil {
				t.Fatalf("invalid list accepted: %#v, %v", addresses, err)
			}
			// A bad list must be rejected before dialing either SMTP transport.
			for _, port := range []string{"1", "465"} {
				err := sendMail(map[string]string{"host": "127.0.0.1", "port": port, "from": "sender@example.test", "to": input}, "", Message{})
				if err == nil || (err.Error() != "invalid email recipients" && err.Error() != "email needs host, from, to") {
					t.Fatalf("port %s: got %v", port, err)
				}
			}
		})
	}
}

func TestSendMailNormalizesRecipients(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type delivery struct {
		commands []string
		data     string
		err      error
	}
	received := make(chan delivery, 1)
	go func() {
		var result delivery
		defer func() { received <- result }()
		conn, err := listener.Accept()
		if err != nil {
			result.err = err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		wire := textproto.NewConn(conn)
		if err := wire.PrintfLine("220 localhost ESMTP"); err != nil {
			result.err = err
			return
		}
		for {
			line, err := wire.ReadLine()
			if err != nil {
				result.err = err
				return
			}
			result.commands = append(result.commands, line)
			switch {
			case strings.HasPrefix(line, "DATA"):
				_ = wire.PrintfLine("354 send message")
				data, err := wire.ReadDotBytes()
				if err != nil {
					result.err = err
					return
				}
				result.data = string(data)
				_ = wire.PrintfLine("250 accepted")
			case strings.HasPrefix(line, "QUIT"):
				_ = wire.PrintfLine("221 bye")
				return
			default:
				_ = wire.PrintfLine("250 OK")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	err = sendMail(map[string]string{"host": host, "port": port, "from": `Sender <"sender,alerts"@example.test>`, "to": `"Doe, Jane" <jane@example.test>; "Team; Alerts" <alerts@example.test>; "quoted,local"@example.test`}, "", Message{Title: "test", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	result := <-received
	if result.err != nil {
		t.Fatal(result.err)
	}
	var envelopes []string
	for _, command := range result.commands {
		if strings.HasPrefix(command, "MAIL FROM:") || strings.HasPrefix(command, "RCPT TO:") {
			envelopes = append(envelopes, command)
		}
	}
	want := []string{`MAIL FROM:<"sender,alerts"@example.test>`, "RCPT TO:<jane@example.test>", "RCPT TO:<alerts@example.test>", `RCPT TO:<"quoted,local"@example.test>`}
	if !reflect.DeepEqual(envelopes, want) {
		t.Fatalf("envelopes: %#v", envelopes)
	}
	message, err := mail.ReadMessage(strings.NewReader(result.data))
	if err != nil {
		t.Fatal(err)
	}
	addresses, err := message.Header.AddressList("To")
	if err != nil {
		t.Fatalf("invalid To header: %q: %v", message.Header.Get("To"), err)
	}
	if len(addresses) != 3 || addresses[0].Name != "Doe, Jane" || addresses[1].Name != "Team; Alerts" {
		t.Fatalf("To: %#v", addresses)
	}
}

func TestSendMailRejectsInvalidSenderBeforeDial(t *testing.T) {
	for _, from := range []string{"invalid", "a@example.test, b@example.test", "a@example.test\r\nBcc: b@example.test"} {
		for _, port := range []string{"1", "465"} {
			err := sendMail(map[string]string{"host": "127.0.0.1", "port": port, "from": from, "to": "valid@example.test"}, "", Message{})
			if err == nil || err.Error() != "invalid email sender" {
				t.Fatalf("port %s sender %q: %v", port, from, err)
			}
		}
	}
}
