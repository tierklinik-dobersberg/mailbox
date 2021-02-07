package mailbox

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	"github.com/mxk/go-imap/imap"
	// Make sure we have the charset data available
	_ "github.com/paulrosania/go-charset/data"
)

// EMail represents a parsed E-Mail fetched from a mailbox.
type EMail struct {
	MultiPart
	// Raw provides raw access to the mail headers and the
	// body reader.
	Raw *mail.Message `json:"-"`
	// From holds the parsed FROM envelop.
	From *mail.Address `json:"from"`
	// To holds a parsed address list of the receipients.
	To []*mail.Address `json:"to"`
	// InternalDate is the date at which the email was received
	// by the mailbox.
	InternalDate time.Time `json:"internalDate"`
	// Precedence holds the precedence header value.
	Precedence string `json:"precedence"`
	// Subject holds the decoded subject of the mail.
	Subject string `json:"subject"`
	// UID is the mailbox specific UID value that identifies
	// this mail. Note that UID is only valid as long as the
	// mailbox UIDVALIDITY has changed.
	UID uint32 `json:"uid"`
}

// MailFromFields creates a EMail from a set of IMAP fields. It expects
// RFC822.HEADER, BODY[], INTERNALDATE and UID fields to be set.
func MailFromFields(ctx context.Context, fields imap.FieldMap) (*EMail, error) {
	// copy the email in it's raw form to a buffer
	rawMail := new(bytes.Buffer)
	rawMail.Write(imap.AsBytes(fields["RFC822.HEADER"]))
	rawMail.Write([]byte("\n\n"))
	rawBody := imap.AsBytes(fields["BODY[]"])
	rawMail.Write(rawBody)

	m, err := mail.ReadMessage(rawMail)
	if err != nil {
		return nil, fmt.Errorf("parsing mail: %w", err)
	}

	from, err := mail.ParseAddress(m.Header.Get("From"))
	if err != nil {
		return nil, fmt.Errorf("parsing From: %w", err)
	}

	to, err := m.Header.AddressList("To")
	if err != nil {
		return nil, fmt.Errorf("parsing To: %w", err)
	}

	result := &EMail{
		Raw:          m,
		InternalDate: imap.AsDateTime(fields["INTERNALDATE"]),
		Precedence:   m.Header.Get("Precedence"),
		From:         from,
		To:           to,
		Subject:      decodeString(m.Header.Get("Subject")),
		UID:          imap.AsNumber(fields["UID"]),
	}

	parsed, err := ParseMIMEBody(
		ctx,
		textproto.MIMEHeader(m.Header),
		bytes.NewReader(rawBody),
	)
	if err != nil {
		return result, fmt.Errorf("parsing body: %w", err)
	}
	result.MultiPart = *parsed

	return result, nil
}

func hasEncoding(word string) bool {
	return strings.Contains(word, "=?") && strings.Contains(word, "?=")
}

func isEncodedWord(word string) bool {
	return strings.HasPrefix(word, "=?") && strings.HasSuffix(word, "?=") && strings.Count(word, "?") == 4
}

func decodeString(subject string) string {
	if !hasEncoding(subject) {
		return subject
	}

	dec := mime.WordDecoder{}
	sub, _ := dec.DecodeHeader(subject)
	return sub
}
