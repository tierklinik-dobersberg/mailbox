package mailbox

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"strings"

	"github.com/paulrosania/go-charset/charset"
	"github.com/sloonz/go-qprintable"
	"github.com/tierklinik-dobersberg/logger"
)

// MultiPart is a multi-part email.
type MultiPart struct {
	// MimeType is the parsed mime-type of this message part.
	MimeType string `json:"mimeType,omitempty"`
	// FileName is the name of the file as advertised by the
	// Content-Disposition header.
	FileName string `json:"filename,omitempty"`
	// Inline is set to true if this multi-part is sent as
	// inline in the Content-Disposition header. If false,
	// the Content-Disposition header has been set to "attachment".
	Inline bool `json:"inline,omitempty"`
	// Children holds all nested multipart message parts.
	// Only set if MimeType starts with multipart/.
	// Mutally exclusive with Body.
	Children []MultiPart `json:"children,omitempty"`
	// Body is the actual body of the multipart message. Only set
	// if this part is not a multipart message by itself.
	Body []byte `json:"body,omitempty"`
}

// IsMultiPart returns true if mp is a multipart message and may
// contain nested children.
func (mp *MultiPart) IsMultiPart() bool {
	return strings.HasPrefix(mp.MimeType, "multipart/")
}

// FindByMIME searches for the body parts that matches mimeType.
// The mimeType may search for wildcard by using "*" for one or both
// parts of the mimetype. For example, "image/*" searches for all images
// while "*/*" returns everything.
func (mp *MultiPart) FindByMIME(mimeType string) []*MultiPart {
	var resultSet []*MultiPart

	if mimeType == "*" {
		resultSet = append(resultSet, mp)
		for _, child := range mp.Children {
			resultSet = append(resultSet, child.FindByMIME(mimeType)...)
		}
	}

	matchParts := strings.SplitN(mimeType, "/", 2)
	if len(matchParts) != 2 {
		return nil
	}

	var matches bool
	parts := strings.SplitN(mp.MimeType, "/", 2)
	matches = len(parts) == len(matchParts) && (matchParts[0] == "*" || matchParts[0] == parts[0]) && (matchParts[1] == "*" || matchParts[1] == parts[1])

	if matches {
		resultSet = append(resultSet, mp)
	}

	for _, child := range mp.Children {
		resultSet = append(resultSet, child.FindByMIME(mimeType)...)
	}

	return resultSet
}

// FindByFilename returns all multipart parts that have the filename name
// advertised in the Content-Disposition MIME header.
func (mp *MultiPart) FindByFilename(name string) []*MultiPart {
	var resultSet []*MultiPart

	if mp.FileName == name {
		resultSet = append(resultSet, mp)
	}

	for _, child := range mp.Children {
		resultSet = append(resultSet, child.FindByFilename(name)...)
	}

	return resultSet
}

// FindByFilenameRegex is like FindByFilename but accepts a regular
// expression instead of a fixed name.
func (mp *MultiPart) FindByFilenameRegex(re *regexp.Regexp) []*MultiPart {
	var resultSet []*MultiPart

	if re.MatchString(mp.FileName) {
		resultSet = append(resultSet, mp)
	}

	for _, child := range mp.Children {
		resultSet = append(resultSet, child.FindByFilenameRegex(re)...)
	}

	return resultSet
}

// ParseMIMEBody parses the MIME payload from rawBody and partHeader. It supports
// parsing nested multipart MIME payloads.
func ParseMIMEBody(ctx context.Context, partHeader textproto.MIMEHeader, rawBody io.Reader) (*MultiPart, error) {
	var result = new(MultiPart)

	// Parse Content-Type header.
	mimeType, mimeParams, err := mime.ParseMediaType(
		partHeader.Get("Content-Type"),
	)
	if err != nil {
		return nil, fmt.Errorf("parsing Content-Type: %w", err)
	}
	result.MimeType = mimeType

	// Parse and extract data from Content-Disposition header
	if contentDisposition := partHeader.Get("Content-Disposition"); contentDisposition != "" {
		disposition, dispositionParams, err := mime.ParseMediaType(
			partHeader.Get("Content-Disposition"),
		)
		if err != nil {
			return result, fmt.Errorf("parsing Content-Disposition: %w", err)
		}
		result.FileName = decodeString(dispositionParams["filename"])
		result.Inline = disposition == "inline"
	}

	// decode the body
	encoding := partHeader.Get("Content-Transfer-Encoding")
	bodyReader, err := decodeBody(mimeParams["charset"], encoding, rawBody)
	if err != nil {
		return result, fmt.Errorf("failed to decode body: %w", err)
	}

	if strings.HasPrefix(mimeType, "multipart/") {
		mr := multipart.NewReader(bodyReader, mimeParams["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return result, err
			}

			child, err := ParseMIMEBody(ctx, p.Header, p)
			if err != nil {
				logger.Errorf(ctx, "failed to parse part: %s", err)
				continue
			}
			result.Children = append(result.Children, *child)
		}
	} else {
		body, err := ioutil.ReadAll(bodyReader)
		if err != nil {
			return result, err
		}
		result.Body = body
	}

	return result, nil
}

func decodeBody(charsetStr, encoding string, body io.Reader) (io.Reader, error) {
	var reader io.Reader = body
	if strings.ToLower(charsetStr) == "iso-8859-1" {
		var err error
		reader, err = charset.NewReader("latin1", reader)
		if err != nil {
			return nil, err
		}
	}

	switch strings.ToLower(encoding) {
	case "", "7bit":
	case "quoted-printable":
		// TODO(ppacher): multipart.Reader.NextPart() transparently converts
		// a quoted-printable already so we might get rid of this one
		reader = qprintable.NewDecoder(
			qprintable.WindowsTextEncoding,
			reader,
		)
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, reader)
	default:
		return nil, fmt.Errorf("unsupported encoding %q", encoding)
	}

	return reader, nil
}
