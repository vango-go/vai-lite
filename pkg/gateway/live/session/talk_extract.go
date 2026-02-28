package session

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type talkJSONContainerKind int

const (
	talkContainerObject talkJSONContainerKind = iota + 1
	talkContainerArray
)

type talkJSONObjectState int

const (
	talkObjExpectKeyOrEnd talkJSONObjectState = iota + 1
	talkObjExpectColon
	talkObjExpectValue
	talkObjExpectCommaOrEnd
)

type talkJSONArrayState int

const (
	talkArrExpectValueOrEnd talkJSONArrayState = iota + 1
	talkArrExpectCommaOrEnd
)

type talkJSONContainer struct {
	kind talkJSONContainerKind
	obj  talkJSONObjectState
	arr  talkJSONArrayState
	key  string
}

// TalkTextExtractor incrementally extracts the decoded `"text"` JSON string value
// from a streaming tool-call arguments object like `{"text":"..."}`.
//
// It is resilient to arbitrary chunk splits, including mid-escape and mid-`\uXXXX`.
// Feed expects appended partial JSON (like provider `input_json_delta`) and returns
// only newly decoded text safe for captions/TTS.
type TalkTextExtractor struct {
	buf []byte
	pos int

	stack []talkJSONContainer

	inString     bool
	stringIsKey  bool
	stringIsText bool

	keyBuilder strings.Builder
	textSoFar  strings.Builder

	dec jsonStringDecoder
}

func (e *TalkTextExtractor) Feed(partialJSON string) (string, error) {
	if partialJSON == "" {
		return "", nil
	}
	e.buf = append(e.buf, partialJSON...)

	var newly strings.Builder

	for e.pos < len(e.buf) {
		b := e.buf[e.pos]

		if e.inString {
			decoded, n, done, err := e.dec.Consume(e.buf[e.pos:])
			if err != nil {
				return "", err
			}
			if n == 0 {
				// Need more input
				break
			}
			e.pos += n

			if decoded != "" {
				if e.stringIsText {
					e.textSoFar.WriteString(decoded)
					newly.WriteString(decoded)
				} else if e.stringIsKey {
					e.keyBuilder.WriteString(decoded)
				}
			}
			if done {
				e.inString = false
				if e.stringIsKey {
					e.setLastKey(e.keyBuilder.String())
					e.keyBuilder.Reset()
				} else {
					// Any completed string token that is a value should advance the container state.
					e.advancePrimitiveStart()
				}
				if e.stringIsText {
					// Finished the text string value; allow parsing to continue but keep extracted text.
				}
				e.stringIsKey = false
				e.stringIsText = false
			}
			continue
		}

		switch b {
		case ' ', '\t', '\r', '\n':
			e.pos++
			continue
		case '{':
			e.pushObject()
			e.pos++
			continue
		case '[':
			e.pushArray()
			e.pos++
			continue
		case '}':
			e.popContainer(talkContainerObject)
			e.pos++
			continue
		case ']':
			e.popContainer(talkContainerArray)
			e.pos++
			continue
		case ',':
			e.advanceComma()
			e.pos++
			continue
		case ':':
			e.advanceColon()
			e.pos++
			continue
		case '"':
			// Start of a JSON string token.
			e.pos++
			e.dec.Reset()
			e.inString = true
			e.stringIsKey = e.expectingKey()
			e.stringIsText = e.expectingTextString()
			if e.stringIsKey {
				e.keyBuilder.Reset()
			}
			continue
		default:
			// Skip non-string primitive tokens; we only care about `"text":"..."`.
			e.advancePrimitiveStart()
			e.pos++
			continue
		}
	}

	if e.pos > 0 {
		// Drop processed prefix but keep any partial string/escape state in decoder.
		e.buf = append([]byte(nil), e.buf[e.pos:]...)
		e.pos = 0
	}

	return newly.String(), nil
}

func (e *TalkTextExtractor) FullText() string {
	return e.textSoFar.String()
}

func (e *TalkTextExtractor) pushObject() {
	if len(e.stack) == 0 {
		e.stack = append(e.stack, talkJSONContainer{kind: talkContainerObject, obj: talkObjExpectKeyOrEnd})
		return
	}
	top := e.stack[len(e.stack)-1]
	switch top.kind {
	case talkContainerObject:
		if top.obj == talkObjExpectValue {
			e.stack = append(e.stack, talkJSONContainer{kind: talkContainerObject, obj: talkObjExpectKeyOrEnd})
			// Parent consumed a value.
			top.obj = talkObjExpectCommaOrEnd
			top.key = ""
			e.stack[len(e.stack)-2] = top
			return
		}
	case talkContainerArray:
		if top.arr == talkArrExpectValueOrEnd {
			e.stack = append(e.stack, talkJSONContainer{kind: talkContainerObject, obj: talkObjExpectKeyOrEnd})
			top.arr = talkArrExpectCommaOrEnd
			e.stack[len(e.stack)-2] = top
			return
		}
	}
	e.stack = append(e.stack, talkJSONContainer{kind: talkContainerObject, obj: talkObjExpectKeyOrEnd})
}

func (e *TalkTextExtractor) pushArray() {
	if len(e.stack) == 0 {
		e.stack = append(e.stack, talkJSONContainer{kind: talkContainerArray, arr: talkArrExpectValueOrEnd})
		return
	}
	top := e.stack[len(e.stack)-1]
	switch top.kind {
	case talkContainerObject:
		if top.obj == talkObjExpectValue {
			e.stack = append(e.stack, talkJSONContainer{kind: talkContainerArray, arr: talkArrExpectValueOrEnd})
			top.obj = talkObjExpectCommaOrEnd
			top.key = ""
			e.stack[len(e.stack)-2] = top
			return
		}
	case talkContainerArray:
		if top.arr == talkArrExpectValueOrEnd {
			e.stack = append(e.stack, talkJSONContainer{kind: talkContainerArray, arr: talkArrExpectValueOrEnd})
			top.arr = talkArrExpectCommaOrEnd
			e.stack[len(e.stack)-2] = top
			return
		}
	}
	e.stack = append(e.stack, talkJSONContainer{kind: talkContainerArray, arr: talkArrExpectValueOrEnd})
}

func (e *TalkTextExtractor) popContainer(kind talkJSONContainerKind) {
	if len(e.stack) == 0 {
		return
	}
	top := e.stack[len(e.stack)-1]
	if top.kind != kind {
		return
	}
	e.stack = e.stack[:len(e.stack)-1]
}

func (e *TalkTextExtractor) advanceComma() {
	if len(e.stack) == 0 {
		return
	}
	top := e.stack[len(e.stack)-1]
	switch top.kind {
	case talkContainerObject:
		if top.obj == talkObjExpectCommaOrEnd {
			top.obj = talkObjExpectKeyOrEnd
			top.key = ""
			e.stack[len(e.stack)-1] = top
		}
	case talkContainerArray:
		if top.arr == talkArrExpectCommaOrEnd {
			top.arr = talkArrExpectValueOrEnd
			e.stack[len(e.stack)-1] = top
		}
	}
}

func (e *TalkTextExtractor) advanceColon() {
	if len(e.stack) == 0 {
		return
	}
	top := e.stack[len(e.stack)-1]
	if top.kind == talkContainerObject && top.obj == talkObjExpectColon {
		top.obj = talkObjExpectValue
		e.stack[len(e.stack)-1] = top
	}
}

func (e *TalkTextExtractor) advancePrimitiveStart() {
	if len(e.stack) == 0 {
		return
	}
	top := e.stack[len(e.stack)-1]
	switch top.kind {
	case talkContainerObject:
		if top.obj == talkObjExpectValue {
			top.obj = talkObjExpectCommaOrEnd
			top.key = ""
			e.stack[len(e.stack)-1] = top
		}
	case talkContainerArray:
		if top.arr == talkArrExpectValueOrEnd {
			top.arr = talkArrExpectCommaOrEnd
			e.stack[len(e.stack)-1] = top
		}
	}
}

func (e *TalkTextExtractor) expectingKey() bool {
	if len(e.stack) == 0 {
		// Allow implicit object parsing if we see a string at the start.
		e.stack = append(e.stack, talkJSONContainer{kind: talkContainerObject, obj: talkObjExpectKeyOrEnd})
		return true
	}
	top := e.stack[len(e.stack)-1]
	return top.kind == talkContainerObject && top.obj == talkObjExpectKeyOrEnd
}

func (e *TalkTextExtractor) expectingTextString() bool {
	if len(e.stack) == 0 {
		return false
	}
	top := e.stack[len(e.stack)-1]
	return top.kind == talkContainerObject && top.obj == talkObjExpectValue && strings.EqualFold(top.key, "text")
}

func (e *TalkTextExtractor) setLastKey(key string) {
	if len(e.stack) == 0 {
		return
	}
	top := e.stack[len(e.stack)-1]
	if top.kind != talkContainerObject || top.obj != talkObjExpectKeyOrEnd {
		return
	}
	top.key = key
	top.obj = talkObjExpectColon
	e.stack[len(e.stack)-1] = top
}

// jsonStringDecoder incrementally decodes a JSON string body (characters between quotes).
// It consumes input until it hits an unescaped `"` which marks end-of-string.
type jsonStringDecoder struct {
	inEscape bool

	expectLowPrefix int // 0 none; 1 expect '\\'; 2 expect 'u'
	pendingHigh     uint16

	unicodeDigits int
	unicodeVal    uint16

	utf8Buf []byte
}

func (d *jsonStringDecoder) Reset() {
	d.inEscape = false
	d.expectLowPrefix = 0
	d.pendingHigh = 0
	d.unicodeDigits = 0
	d.unicodeVal = 0
	d.utf8Buf = d.utf8Buf[:0]
}

func (d *jsonStringDecoder) Consume(data []byte) (decoded string, n int, done bool, err error) {
	var out strings.Builder
	i := 0
	for i < len(data) {
		b := data[i]

		if d.unicodeDigits > 0 {
			v, ok := fromHex(b)
			if !ok {
				return "", 0, false, fmt.Errorf("invalid unicode escape")
			}
			d.unicodeVal = (d.unicodeVal << 4) | uint16(v)
			d.unicodeDigits--
			i++
			if d.unicodeDigits == 0 {
				r := rune(d.unicodeVal)
				d.unicodeVal = 0
				if d.pendingHigh != 0 {
					low := uint16(r)
					high := d.pendingHigh
					d.pendingHigh = 0
					if low < 0xDC00 || low > 0xDFFF {
						return "", 0, false, fmt.Errorf("invalid surrogate pair")
					}
					out.WriteRune(utf16.DecodeRune(rune(high), rune(low)))
					d.expectLowPrefix = 0
					continue
				}
				if r >= 0xD800 && r <= 0xDBFF {
					d.pendingHigh = uint16(r)
					d.expectLowPrefix = 1
					continue
				}
				if r >= 0xDC00 && r <= 0xDFFF {
					return "", 0, false, fmt.Errorf("unexpected low surrogate")
				}
				out.WriteRune(r)
			}
			continue
		}

		if d.expectLowPrefix != 0 {
			if d.expectLowPrefix == 1 {
				if b != '\\' {
					return "", 0, false, fmt.Errorf("expected low surrogate escape")
				}
				d.expectLowPrefix = 2
				i++
				continue
			}
			if d.expectLowPrefix == 2 {
				if b != 'u' {
					return "", 0, false, fmt.Errorf("expected low surrogate unicode escape")
				}
				d.expectLowPrefix = 0
				d.unicodeDigits = 4
				d.unicodeVal = 0
				i++
				continue
			}
		}

		if len(d.utf8Buf) > 0 {
			d.utf8Buf = append(d.utf8Buf, b)
			i++
			if !utf8.FullRune(d.utf8Buf) {
				continue
			}
			r, size := utf8.DecodeRune(d.utf8Buf)
			if r == utf8.RuneError && size == 1 {
				return "", 0, false, fmt.Errorf("invalid utf-8")
			}
			out.WriteRune(r)
			d.utf8Buf = d.utf8Buf[:0]
			continue
		}

		if d.inEscape {
			switch b {
			case '"', '\\', '/':
				out.WriteByte(b)
			case 'b':
				out.WriteByte('\b')
			case 'f':
				out.WriteByte('\f')
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			case 'u':
				d.unicodeDigits = 4
				d.unicodeVal = 0
				// do not emit yet
			default:
				return "", 0, false, fmt.Errorf("invalid escape")
			}
			d.inEscape = false
			i++
			continue
		}

		switch b {
		case '\\':
			d.inEscape = true
			i++
			continue
		case '"':
			if d.pendingHigh != 0 {
				return "", 0, false, fmt.Errorf("incomplete surrogate pair")
			}
			if len(d.utf8Buf) > 0 {
				return "", 0, false, fmt.Errorf("incomplete utf-8")
			}
			return out.String(), i + 1, true, nil
		default:
			if b < 0x20 {
				return "", 0, false, fmt.Errorf("invalid control char in string")
			}
			if b < 0x80 {
				out.WriteByte(b)
				i++
				continue
			}
			d.utf8Buf = append(d.utf8Buf, b)
			i++
			continue
		}
	}

	return out.String(), i, false, nil
}

func fromHex(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
