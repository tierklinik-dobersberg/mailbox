package mailbox

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/mxk/go-imap/imap"
)

// IMAPDateFormat is the date format used for IMAP SINCE.
const IMAPDateFormat = "02-Jan-2006"

// Client is a mailbox client.
type Client struct {
	// IMAP holds the actual IMAP client
	IMAP *imap.Client
}

// Connect returns a new IMAP client for the mailbox configured
// in info.
func Connect(info Config) (*Client, error) {
	var (
		client *imap.Client
		err    error
	)
	if info.TLS {
		config := new(tls.Config)
		config.InsecureSkipVerify = info.InsecureSkipVerify
		client, err = imap.DialTLS(info.Host, config)
	} else {
		client, err = imap.Dial(info.Host)
	}

	if err != nil {
		return nil, fmt.Errorf("dialing: %w", err)
	}

	if info.User != "" {
		if _, err := client.Login(info.User, info.Password); err != nil {
			return nil, fmt.Errorf("authenticating: %w", err)
		}
	}

	if _, err := imap.Wait(client.Select(info.Folder, info.ReadOnly)); err != nil {
		return nil, fmt.Errorf("selecting mailbox %q: %w", info.Folder, err)
	}

	return &Client{
		IMAP: client,
	}, nil
}

// SearchUIDs performs an IMAP UIDSearch on cli and supports searching mails that arrived
// since a given time. If since is the zero time value it will be ignored.
func (cli *Client) SearchUIDs(search string, since time.Time) ([]uint32, error) {
	var specs []imap.Field
	if len(search) > 0 {
		specs = append(specs, search)
	}

	if !since.IsZero() {
		sinceStr := since.Format(IMAPDateFormat)
		specs = append(specs, "SINCE", sinceStr)
	}

	cmd, err := imap.Wait(cli.IMAP.UIDSearch(specs...))
	if err != nil {
		return nil, err
	}

	var uids []uint32

	for _, data := range cmd.Data {
		uids = append(uids, data.SearchResults()...)
	}

	return uids, nil
}

// Response is streamed by FetchUIDs for each mail or error encountered.
type Response struct {
	*EMail `json:",omitempty"`
	Err    error `json:"error,omitempty"`
}

// FetchUIDs fetches all mail UIDs specified in the sequence set seq.
func (cli *Client) FetchUIDs(ctx context.Context, seq *imap.SeqSet) (chan Response, error) {
	ch := make(chan Response, 100)
	if seq.Empty() {
		close(ch)
		return ch, nil
	}

	fetchCommand, err := imap.Wait(
		cli.IMAP.UIDFetch(
			seq,
			"INTERNALDATE",
			"BODY[]",
			"UID",
			"RFC822.HEADER",
		),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching mails: %w", err)
	}

	go func() {
		defer close(ch)
		for _, msgData := range fetchCommand.Data {
			msgFields := msgData.MessageInfo().Attrs

			// make sure is a legit response before we attempt to parse it
			// deal with unsolicited FETCH responses containing only flags
			// I'm lookin' at YOU, Gmail!
			// http://mailman13.u.washington.edu/pipermail/imap-protocol/2014-October/002355.html
			// http://stackoverflow.com/questions/26262472/gmail-imap-is-sometimes-returning-bad-results-for-fetch
			if _, ok := msgFields["RFC822.HEADER"]; !ok {
				continue
			}

			mail, err := MailFromFields(ctx, msgFields)
			ch <- Response{
				EMail: mail,
				Err:   err,
			}
		}
	}()

	return ch, nil
}
