package parsemail

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

const contentTypeMultipartMixed = "multipart/mixed"
const contentTypeMultipartAlternative = "multipart/alternative"
const contentTypeMultipartRelated = "multipart/related"
const contentTypeMultipartSigned = "multipart/signed"
const contentTypeTextHtml = "text/html"
const contentTypeTextPlain = "text/plain"

// this is a "special" content-type
const contentAttachment = "attachment"

const errInvalidWord = "mime: invalid RFC 2047 encoded-word"

// Parse an email message read from io.Reader into parsemail.Email struct
func Parse(r io.Reader) (email Email, err error) {
	msg, err := mail.ReadMessage(r)
	if err != nil {
		return 
	}

	email, err = createEmailFromHeader(msg.Header)
	if err != nil {
		return
	}

	email.ContentType = msg.Header.Get("Content-Type")
	contentType, params, err := parseContentType(email.ContentType)
	if err != nil {
		return
	}

	switch contentType {
	case contentTypeMultipartMixed:
		email.TextBody, email.HTMLBody, email.Attachments, email.EmbeddedFiles, err = parseMultipartMixed(msg.Body, params["boundary"])
	case contentTypeMultipartAlternative:
		email.TextBody, email.HTMLBody, email.EmbeddedFiles, err = parseMultipartAlternative(msg.Body, params["boundary"])
	case contentTypeMultipartRelated:
		email.TextBody, email.HTMLBody, email.EmbeddedFiles, err = parseMultipartRelated(msg.Body, params["boundary"])
	case contentTypeMultipartSigned:
		email.TextBody, email.HTMLBody, email.Attachments, email.EmbeddedFiles, err = parseMultipartSigned(msg.Body, params["boundary"])
	case contentTypeTextPlain:
		message, _ := ioutil.ReadAll(msg.Body)
		var reader io.Reader
		reader, err = decodeContent(strings.NewReader(string(message[:])), msg.Header.Get("Content-Transfer-Encoding"))
		if err != nil {
			return
		}

		message, err = ioutil.ReadAll(reader)
		if err != nil {
			return
		}

		email.TextBody = strings.TrimSuffix(string(message[:]), "\n")
	case contentTypeTextHtml:
		message, _ := ioutil.ReadAll(msg.Body)
		var reader io.Reader
		reader, err = decodeContent(strings.NewReader(string(message[:])), msg.Header.Get("Content-Transfer-Encoding"))
		if err != nil {
			return
		}

		message, err = ioutil.ReadAll(reader)
		if err != nil {
			return
		}

		email.HTMLBody = strings.TrimSuffix(string(message[:]), "\n")
	default:
		email.Content, err = decodeContent(msg.Body, msg.Header.Get("Content-Transfer-Encoding"))
		if err != nil {
                        return
                }
	}

	return
}

func createEmailFromHeader(header mail.Header) (email Email, err error) {
	hp := headerParser{header: &header}

	email.Subject = decodeMimeSentence(header.Get("Subject"))
	email.From = hp.parseAddressList(header.Get("From"))
	email.Sender = hp.parseAddress(header.Get("Sender"))
	email.ReplyTo = hp.parseAddressList(header.Get("Reply-To"))
	email.To = hp.parseAddressList(header.Get("To"))
	email.Cc = hp.parseAddressList(header.Get("Cc"))
	email.Bcc = hp.parseAddressList(header.Get("Bcc"))
	email.Date = hp.parseTime(header.Get("Date"))
	email.ResentFrom = hp.parseAddressList(header.Get("Resent-From"))
	email.ResentSender = hp.parseAddress(header.Get("Resent-Sender"))
	email.ResentTo = hp.parseAddressList(header.Get("Resent-To"))
	email.ResentCc = hp.parseAddressList(header.Get("Resent-Cc"))
	email.ResentBcc = hp.parseAddressList(header.Get("Resent-Bcc"))
	email.ResentMessageID = hp.parseMessageId(header.Get("Resent-Message-ID"))
	email.MessageID = hp.parseMessageId(header.Get("Message-ID"))
	email.InReplyTo = hp.parseMessageIdList(header.Get("In-Reply-To"))
	email.References = hp.parseMessageIdList(header.Get("References"))
	email.ResentDate = hp.parseTime(header.Get("Resent-Date"))

	if hp.err != nil {
		err = hp.err
		return
	}

	//decode whole header for easier access to extra fields
	//todo: should we decode? aren't only standard fields mime encoded?
	email.Header, err = decodeHeaderMime(header)
	if err != nil {
		return
	}

	return
}

func parseContentType(contentTypeHeader string) (contentType string, params map[string]string, err error) {
	if contentTypeHeader == "" {
		contentType = contentTypeTextPlain
		return
	}

	return mime.ParseMediaType(contentTypeHeader)
}

func parseMultipartRelated(msg io.Reader, boundary string) (textBody, htmlBody string, embeddedFiles []EmbeddedFile, err error) {
	pmr := multipart.NewReader(msg, boundary)
	for {
		part, err := pmr.NextRawPart()

		if err == io.EOF {
			break
		} else if err != nil {
			return textBody, htmlBody, embeddedFiles, err
		}

		contentType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			return textBody, htmlBody, embeddedFiles, err
		}

		switch contentType {
		case contentTypeTextPlain:
			message, _ := ioutil.ReadAll(part)
			reader, err := decodeContent(strings.NewReader(string(message[:])), part.Header.Get("Content-Transfer-Encoding"))
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			ppContent, err := ioutil.ReadAll(reader)
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			textBody += strings.TrimSuffix(string(ppContent[:]), "\n")
		case contentTypeTextHtml:
			message, _ := ioutil.ReadAll(part)
			reader, err := decodeContent(strings.NewReader(string(message[:])), part.Header.Get("Content-Transfer-Encoding"))
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			ppContent, err := ioutil.ReadAll(reader)
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			htmlBody += strings.TrimSuffix(string(ppContent[:]), "\n")
		case contentTypeMultipartAlternative:
			tb, hb, ef, err := parseMultipartAlternative(part, params["boundary"])
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			htmlBody += hb
			textBody += tb
			embeddedFiles = append(embeddedFiles, ef...)
		default:
			if isEmbeddedFile(part) {
				ef, err := decodeEmbeddedFile(part)
				if err != nil {
					return textBody, htmlBody, embeddedFiles, err
				}

				embeddedFiles = append(embeddedFiles, ef)
			} else {
				return textBody, htmlBody, embeddedFiles, fmt.Errorf("Can't process multipart/related inner mime type: %s", contentType)
			}
		}
	}

	return textBody, htmlBody, embeddedFiles, err
}

func parseMultipartAlternative(msg io.Reader, boundary string) (textBody, htmlBody string, embeddedFiles []EmbeddedFile, err error) {
	pmr := multipart.NewReader(msg, boundary)
	for {
		part, err := pmr.NextRawPart()

		if err == io.EOF {
			break
		} else if err != nil {
			return textBody, htmlBody, embeddedFiles, err
		}

		contentType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			return textBody, htmlBody, embeddedFiles, err
		}

		switch contentType {
		case contentTypeTextPlain:
			message, _ := ioutil.ReadAll(part)
			reader, err := decodeContent(strings.NewReader(string(message[:])), part.Header.Get("Content-Transfer-Encoding"))
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			ppContent, err := ioutil.ReadAll(reader)
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			textBody += strings.TrimSuffix(string(ppContent[:]), "\n")
		case contentTypeTextHtml:
			message, _ := ioutil.ReadAll(part)
			reader, err := decodeContent(strings.NewReader(string(message[:])), part.Header.Get("Content-Transfer-Encoding"))
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			ppContent, err := ioutil.ReadAll(reader)
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			htmlBody += strings.TrimSuffix(string(ppContent[:]), "\n")
		case contentTypeMultipartRelated:
			tb, hb, ef, err := parseMultipartRelated(part, params["boundary"])
			if err != nil {
				return textBody, htmlBody, embeddedFiles, err
			}

			htmlBody += hb
			textBody += tb
			embeddedFiles = append(embeddedFiles, ef...)
		default:
			if isEmbeddedFile(part) {
				ef, err := decodeEmbeddedFile(part)
				if err != nil {
					return textBody, htmlBody, embeddedFiles, err
				}

				embeddedFiles = append(embeddedFiles, ef)
			} else {
				return textBody, htmlBody, embeddedFiles, fmt.Errorf("Can't process multipart/alternative inner mime type: %s", contentType)
			}
		}
	}

	return textBody, htmlBody, embeddedFiles, err
}

func parseMultipartMixed(msg io.Reader, boundary string) (textBody, htmlBody string, attachments []Attachment, embeddedFiles []EmbeddedFile, err error) {
	mr := multipart.NewReader(msg, boundary)

	for {
		part, err := mr.NextRawPart()
		if err == io.EOF {
			err = nil
			break
		} else if err != nil {
			return textBody, htmlBody, attachments, embeddedFiles, err
		}

		contentType, params, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			return textBody, htmlBody, attachments, embeddedFiles, err
		}

		switch contentType {

		case contentTypeMultipartAlternative:
			textBody, htmlBody, embeddedFiles, err = parseMultipartAlternative(part, params["boundary"])
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}

		case contentTypeMultipartRelated:
			textBody, htmlBody, embeddedFiles, err = parseMultipartRelated(part, params["boundary"])
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}

		case contentTypeTextPlain:
			ppContent, err := ioutil.ReadAll(part)
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}

			// decode base64 if encoding is base64
			if strings.ToLower(part.Header.Get("Content-Transfer-Encoding")) == "base64" {
				decoded := make([]byte, base64.StdEncoding.DecodedLen(len(ppContent)))
				_, err := base64.StdEncoding.Decode(decoded, ppContent)
				if err != nil {
					return textBody, htmlBody, attachments, embeddedFiles, err
				}
				textBody += strings.TrimSuffix(string(decoded), "\n")
			} else {
				textBody += strings.TrimSuffix(string(ppContent[:]), "\n")
			}

		case contentTypeTextHtml:
			ppContent, err := ioutil.ReadAll(part)
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}

			// decode base64 if encoding is base64
			if strings.ToLower(part.Header.Get("Content-Transfer-Encoding")) == "base64" {
				decoded := make([]byte, base64.StdEncoding.DecodedLen(len(ppContent)))
				_, err := base64.StdEncoding.Decode(decoded, ppContent)
				if err != nil {
					return textBody, htmlBody, attachments, embeddedFiles, err
				}
				htmlBody += strings.TrimSuffix(string(decoded), "\n")
			} else {
				htmlBody += strings.TrimSuffix(string(ppContent[:]), "\n")
			}

		case isAttachmentAsString(contentType, part):
			at, err := decodeAttachment(part)
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}

			attachments = append(attachments, at)
		default:
			return textBody, htmlBody, attachments, embeddedFiles, fmt.Errorf("Unknown multipart/mixed nested mime type: %s", contentType)
		}
	}

	return textBody, htmlBody, attachments, embeddedFiles, err
}

func parseMultipartSigned(msg io.Reader, boundary string) (textBody, htmlBody string, attachments []Attachment, embeddedFiles []EmbeddedFile, err error) {

	// vars
	err = nil
	var part *multipart.Part
	var contentType string
	var params map[string]string

	// reader
	mr := multipart.NewReader(msg, boundary)

mrparts:
	for {
		part, err = mr.NextPart()
		if err == io.EOF {
			err = nil
			break
		} else if err != nil {
			return
		}

		contentType, params, err = mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			return
		}

		switch contentType {

		case contentTypeMultipartMixed:
			if textBody, htmlBody, attachments, embeddedFiles, err = parseMultipartMixed(part, params["boundary"]); err != nil {
				break mrparts
			}

		case contentTypeMultipartAlternative:
			if textBody, htmlBody, embeddedFiles, err = parseMultipartAlternative(part, params["boundary"]); err != nil {
				break mrparts
			}

		case contentTypeMultipartRelated:
			if textBody, htmlBody, embeddedFiles, err = parseMultipartRelated(part, params["boundary"]); err != nil {
				break mrparts
			}

		case isAttachmentAsString(contentType, part):
			at, err := decodeAttachment(part)
			if err != nil {
				return textBody, htmlBody, attachments, embeddedFiles, err
			}

			attachments = append(attachments, at)

		default:
			err = fmt.Errorf("Unknown multipart/mixed nested mime type: %s", contentType)
			return textBody, htmlBody, attachments, embeddedFiles, err
		}

	}

	return textBody, htmlBody, attachments, embeddedFiles, err
}

func decodeMimeSentence(s string) string {
	result := []string{}
	ss := strings.Split(s, " ")
	var lastWord string

	for i, word := range ss {
		dec := new(mime.WordDecoder)
		w, err := dec.Decode(word)
		if err != nil {
			if len(result) == 0 {
				w = word
			} else {
				// workaround to avoid double spaces from lastWord and current word
				if len(lastWord) > 0 && lastWord[len(lastWord)-1] == 32 {
					w = word
				} else {
					w = " " + word
				}
			}

			// when errInvalidWord, add a spacea only if not is the last word
			if err.Error() == errInvalidWord && i != len(ss)-1 {
				w += " "
			}

		}

		result = append(result, w)
	}

	return strings.Join(result, "")
}

func isAttachmentAsString(contentType string, part *multipart.Part) string {
	if isAttachment(part) == true {
		return contentType
	}

	return ""
}

func decodeHeaderMime(header mail.Header) (mail.Header, error) {
	parsedHeader := map[string][]string{}

	for headerName, headerData := range header {

		parsedHeaderData := []string{}
		for _, headerValue := range headerData {
			parsedHeaderData = append(parsedHeaderData, decodeMimeSentence(headerValue))
		}

		parsedHeader[headerName] = parsedHeaderData
	}

	return mail.Header(parsedHeader), nil
}

func isEmbeddedFile(part *multipart.Part) bool {
	return part.Header.Get("Content-Transfer-Encoding") != ""
}

func decodeEmbeddedFile(part *multipart.Part) (ef EmbeddedFile, err error) {
	cid := decodeMimeSentence(part.Header.Get("Content-Id"))
	decoded, err := decodeContent(part, part.Header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return
	}

	ef.CID = strings.Trim(cid, "<>")
	ef.Data = decoded
	ef.ContentType = part.Header.Get("Content-Type")

	return
}

func isAttachment(part *multipart.Part) bool {
	return part.FileName() != ""
}

func decodeAttachment(part *multipart.Part) (at Attachment, err error) {
	filename := decodeMimeSentence(part.FileName())
	decoded, err := decodeContent(part, part.Header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return
	}

	at.Filename = filename
	at.Data = decoded
	at.ContentType = strings.Split(part.Header.Get("Content-Type"), ";")[0]

	return
}

func decodeContent(content io.Reader, encoding string) (io.Reader, error) {

	encoding = strings.ToLower(encoding)

	switch encoding {
	case "base64":
		decoded := base64.NewDecoder(base64.StdEncoding, content)
		b, err := ioutil.ReadAll(decoded)
		if err != nil {
			return nil, err
		}

		return bytes.NewReader(b), nil
	case "7bit", "8bit", "binary":
		dd, err := ioutil.ReadAll(content)
		if err != nil {
			return nil, err
		}

		return bytes.NewReader(dd), nil
	case "quoted-printable":
		decoded := quotedprintable.NewReader(content)
		b, err := ioutil.ReadAll(decoded)
		if err != nil {
			return nil, err
		}

		return bytes.NewReader(b), nil
	case "":
		return content, nil
	default:
		return nil, fmt.Errorf("unknown encoding: %s", encoding)
	}
}

type headerParser struct {
	header *mail.Header
	err    error
}

func (hp headerParser) parseAddress(s string) (ma *mail.Address) {
	if hp.err != nil {
		return nil
	}

	if strings.Trim(s, " \n") != "" {
		ma, hp.err = mail.ParseAddress(s)

		return ma
	}

	return nil
}

func (hp headerParser) parseAddressList(s string) (ma []*mail.Address) {
	if hp.err != nil {
		return
	}

	if strings.Trim(s, " \n") != "" {
		ma, hp.err = mail.ParseAddressList(s)
		return
	}

	return
}

func (hp headerParser) parseTime(s string) (t time.Time) {
	if hp.err != nil || s == "" {
		return
	}

	formats := []string{
		time.RFC1123Z,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		time.RFC1123Z + " (MST)",
		"Mon, 2 Jan 2006 15:04:05 -0700 (MST)",
	}

	for _, format := range formats {
		t, hp.err = time.Parse(format, s)
		if hp.err == nil {
			return
		}
	}

	return
}

func (hp headerParser) parseMessageId(s string) string {
	if hp.err != nil {
		return ""
	}

	return strings.Trim(s, "<> ")
}

func (hp headerParser) parseMessageIdList(s string) (result []string) {
	if hp.err != nil {
		return
	}

	for _, p := range strings.Split(s, " ") {
		if strings.Trim(p, " \n") != "" {
			result = append(result, hp.parseMessageId(p))
		}
	}

	return
}

// Attachment with filename, content type and data (as a io.Reader)
type Attachment struct {
	Filename    string
	ContentType string
	Data        io.Reader
}

// EmbeddedFile with content id, content type and data (as a io.Reader)
type EmbeddedFile struct {
	CID         string
	ContentType string
	Data        io.Reader
}

// Email with fields for all the headers defined in RFC5322 with it's attachments and
type Email struct {
	Header mail.Header

	Subject    string
	Sender     *mail.Address
	From       []*mail.Address
	ReplyTo    []*mail.Address
	To         []*mail.Address
	Cc         []*mail.Address
	Bcc        []*mail.Address
	Date       time.Time
	MessageID  string
	InReplyTo  []string
	References []string

	ResentFrom      []*mail.Address
	ResentSender    *mail.Address
	ResentTo        []*mail.Address
	ResentDate      time.Time
	ResentCc        []*mail.Address
	ResentBcc       []*mail.Address
	ResentMessageID string

	ContentType string
	Content     io.Reader

	HTMLBody string
	TextBody string

	Attachments   []Attachment
	EmbeddedFiles []EmbeddedFile
}
