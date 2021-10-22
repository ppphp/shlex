package shlex

import (
	"container/list"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type Shlex struct {
	instream                                                                        io.Reader
	infile                                                                          string
	posix                                                                           bool
	punctuation_chars                                                               string
	eof                                                                             *string
	commenters, Wordchars, whitespace, Quotes, escape, escapedquotes, token, source string
	state                                                                           *string
	filestack, pushback, _pushback_chars                                            list.List
	lineno, debug                                                                   int
	whitespace_split                                                                bool
}

func (s *Shlex) push_token(tok string) {
	if s.debug >= 1 {
		print("shlex: pushing token " + tok)
	}
	s.pushback.PushFront(tok)
}

type file struct {
	infile   string
	instream io.Reader
	lineno   int
}

func (s *Shlex) push_source(newstream io.Reader, newfile string) { // ""
	s.filestack.PushFront(&file{infile: s.infile, instream: s.instream, lineno: s.lineno})
	s.infile = newfile
	s.instream = newstream
	s.lineno = 1
	if s.debug > 0 {
		if newfile != "" {
			fmt.Printf("shlex: pushing to file %s\n", s.infile)
		} else {
			fmt.Printf("shlex: pushing to stream %s\n", s.instream)
		}
	}
}

func (s *Shlex) pop_source() error {
	if c, ok := s.instream.(io.ReadCloser); ok {
		if err := c.Close(); err != nil {
			return err
		}
	}
	e := s.filestack.Front()
	f, ok := e.Value.(*file)
	if !ok {
		return fmt.Errorf("type error")
	}
	s.filestack.Remove(e)

	s.infile, s.instream, s.lineno = f.infile, f.instream, f.lineno
	if s.debug != 0 {
		fmt.Printf("shlex: popping to %s, line %d\n", s.instream, s.lineno)
	}
	s.state = new(string)
	*s.state = " "
	return nil
}

func (s *Shlex) GetToken() (string, error) {
	if s.pushback.Len() > 0 {
		e := s.pushback.Back()
		tok, ok := e.Value.(string)
		if !ok {
			return "", fmt.Errorf("type error")
		}
		s.pushback.Remove(e)

		if s.debug >= 1 {
			print("shlex: popping token " + tok)
		}
		return tok, nil
	}
	raw, err := s.ReadRoken()
	if err != nil {
		return "", err
	}
	if s.source != "" {
		for raw == s.source {
			tt, err := s.ReadRoken()
			if err != nil {
				return "", err
			}
			newfile, newstream, err := s.SourceHook(tt)
			if err != nil {
				return "", err
			}
			s.push_source(newstream, newfile)
		}
		raw, err = s.GetToken()
		if err != nil {
			return "", err
		}
	}

	for s.eof != nil && raw == *s.eof {
		if s.filestack.Len() == 0 {
			return *s.eof, nil
		} else {
			s.pop_source()
			raw, err = s.GetToken()
			if err != nil {
				return "", err
			}
		}
	}

	if s.debug >= 1 {
		if s.eof == nil || raw != *s.eof {
			print("shlex: token=" + raw)
		} else {
			print("shlex: token=EOF")
		}
	}
	return raw, nil
}

func (s *Shlex) ReadRoken() (string, error) {
	quoted := false
	escapedstate := " "
	for {
		nextchar := ""
		if s.punctuation_chars != "" && s._pushback_chars.Len() != 0 {
			e := s._pushback_chars.Back()
			ok := false
			nextchar, ok = e.Value.(string)
			if !ok {
				return "", fmt.Errorf("type error")
			}
			s._pushback_chars.Remove(e)
		} else {
			nc := make([]byte, 1)
			if _, err := s.instream.Read(nc); err != nil {
				if err != io.EOF || (s.token == "" && !quoted) {
					return "", err
				}
				nextchar = ""
			} else {
				nextchar = string(nc)
			}
		}
		if nextchar == "\n" {
			s.lineno += 1
		}
		if s.debug >= 3 {
			fmt.Print("shlex: in state %r I see character: %r\n", s.state, nextchar)
		}
		if s.state == nil {
			s.token = ""
			break
		} else if *s.state == " " {
			if nextchar == "" {
				s.state = nil
				break
			} else if strings.Contains(s.whitespace, nextchar) {
				if s.debug >= 2 {
					print("shlex: I see whitespace in whitespace state")
				}
				if s.token != "" || (s.posix && quoted) {
					break
				} else {
					continue
				}
			} else if strings.Contains(s.commenters, nextchar) {
				n := make([]byte, 1)
				for string(n) != "\n" {
					if _, err := s.instream.Read(n); err != nil {
						return "", err
					}
				}
				s.lineno += 1
			} else if s.posix && strings.Contains(s.escape, nextchar) {
				escapedstate = "a"
				s.state = new(string)
				*s.state = nextchar
			} else if strings.Contains(s.Wordchars, nextchar) {
				s.token = nextchar
				s.state = new(string)
				*s.state = "a"
			} else if strings.Contains(s.punctuation_chars, nextchar) {
				s.token = nextchar
				s.state = new(string)
				*s.state = "c"
			} else if strings.Contains(s.Quotes, nextchar) {
				if !s.posix {
					s.token = nextchar
				}
				s.state = new(string)
				*s.state = nextchar
			} else if s.whitespace_split {
				s.token = nextchar
				s.state = new(string)
				*s.state = "a"
			} else {
				s.token = nextchar
				if s.token != "" || (s.posix && quoted) {
					break
				} else {
					continue
				}
			}
		} else if s.state != nil && strings.Contains(s.Quotes, *s.state) {
			quoted = true
			if nextchar == "" {
				if s.debug >= 2 {
					print("shlex: I see EOF in Quotes state")
				}
				return "", fmt.Errorf("No closing quotation")
			}
			if s.state != nil && nextchar == *s.state {
				if !s.posix {
					s.token += nextchar
					s.state = new(string)
					*s.state = " "
					break
				} else {
					s.state = new(string)
					*s.state = "a"
				}
			} else if s.posix && strings.Contains(s.escape, nextchar) && s.state != nil && strings.Contains(s.escapedquotes, *s.state) {
				escapedstate = *s.state
				s.state = new(string)
				*s.state = nextchar
			} else {
				s.token += nextchar
			}
		} else if s.state != nil && strings.Contains(s.escape, *s.state) {
			if nextchar == "" {
				if s.debug >= 2 {
					print("shlex: I see EOF in escape state")
				}
				return "", fmt.Errorf("No escaped character")
			}
			if strings.Contains(s.Quotes, escapedstate) && (s.state == nil || nextchar != *s.state) && nextchar != escapedstate {
				s.token += *s.state
			}
			s.token += nextchar
			s.state = new(string)
			*s.state = escapedstate
		} else if s.state != nil && strings.Contains("ac", *s.state) {
			if nextchar == "" {
				s.state = nil
				break
			} else if strings.Contains(s.whitespace, nextchar) {
				if s.debug >= 2 {
					print("shlex: I see whitespace in word state")
				}
				s.state = new(string)
				*s.state = " "
				if s.token != "" || (s.posix && quoted) {
					break
				} else {
					continue
				}
			} else if strings.Contains(s.commenters, nextchar) {
				n := make([]byte, 1)
				for string(n) != "\n" {
					if _, err := s.instream.Read(n); err != nil {
						return "", err
					}
				}
				s.lineno += 1
				if s.posix {
					s.state = new(string)
					*s.state = " "
					if s.token != "" || (s.posix && quoted) {
						break
					} else {
						continue
					}
				}
			} else if s.state != nil && *s.state == "c" {
				if strings.Contains(
					s.punctuation_chars, nextchar) {
					s.token += nextchar
				} else {
					if !strings.Contains(s.whitespace, nextchar) {
						s._pushback_chars.PushBack(nextchar)
						s.state = new(string)
						*s.state = " "
						break
					}
				}
			} else if s.posix && strings.Contains(s.Quotes, nextchar) {
				s.state = new(string)
				*s.state = nextchar
			} else if s.posix && strings.Contains(s.escape, nextchar) {
				escapedstate = "a"
				s.state = new(string)
				*s.state = nextchar
			} else if strings.Contains(s.Wordchars, nextchar) || strings.Contains(s.Quotes, nextchar) || (s.whitespace_split && !strings.Contains(s.punctuation_chars, nextchar)) {
				s.token += nextchar
			} else {
				if s.punctuation_chars != "" {
					s._pushback_chars.PushBack(nextchar)
				} else {
					s.pushback.PushFront(nextchar)
				}
				if s.debug >= 2 {
					print("shlex: I see punctuation in word state")
				}
				s.state = new(string)
				*s.state = " "
				if s.token != "" || (s.posix && quoted) {
					break
				} else {
					continue
				}
			}
		}
	}

	result := s.token
	s.token = ""
	if s.posix && !quoted && result == "" {
		result = ""
	}
	if s.debug > 1 {
		if result != "" {
			print("shlex: raw token=" + result)
		} else {
			print("shlex: raw token=EOF")
		}
	}
	return result, nil
}

func (s *Shlex) SourceHook(newfile string) (string, *os.File, error) {
	if newfile[0] == '"' && len(newfile) >= 2 {
		newfile = newfile[1 : len(newfile)-1]
	}
	if !filepath.IsAbs(newfile) {
		newfile = path.Join(path.Dir(s.infile), newfile)
	}
	f, err := os.Open(newfile)
	return newfile, f, err
}

func (s *Shlex) error_leader(infile string, lineno int) string { // "", 0
	if infile == "" {
		infile = s.infile
	}
	if lineno == 0 {
		lineno = s.lineno
	}
	return fmt.Sprintf("\"%s\", line %d: ", infile, lineno)
}

func (s *Shlex) __iter__() *Shlex {
	return s
}

func (s *Shlex) __next__() (string, error) {
	token, err := s.GetToken()
	if err != nil {
		return "", err
	}

	if s.eof != nil && token == *s.eof {
		return "", io.EOF
		//raise StopIteration
	}
	return token, nil
}

func Split(s io.Reader, comments, posix bool) ([]string, error) { // false, true
	lex := NewShlex(s, "", posix, "")
	lex.whitespace_split = true
	if !comments {
		lex.commenters = ""
	}
	ret := []string{}
	for {
		token, err := lex.GetToken()
		if err != nil {
			if err != io.EOF {
				return nil, err
			}
			break
		}
		ret = append(ret, token)
	}
	return ret, nil
}

var _find_unsafe = regexp.MustCompile("[^\\w@%+=:,./-]")

func quote(s string) string {
	if s == "" {
		return "''"
	}
	if !_find_unsafe.MatchString(s) {
		return s
	}
	return "'" + strings.Replace(s, "'", "'\"'\"'", -1) + "'"
}

func _print_tokens(lexer *Shlex) error {
	for {
		tt, err := lexer.GetToken()
		if err != nil {
			return err
		}
		if tt == "" {
			break
		}
		print("Token: " + tt)
	}
	return nil
}

func NewShlex(instream io.Reader, infile string, posix bool, punctuation_chars string) *Shlex { // nil, "", false, ""(true is "();<>|&")
	s := &Shlex{}
	if instream != nil {
		s.instream = instream
		s.infile = infile
	} else {
		instream = os.Stdin
		s.infile = ""
	}
	s.posix = posix
	if posix {
		s.eof = nil
	} else {
		s.eof = new(string)
		*s.eof = ""
	}
	s.commenters = "#"
	s.Wordchars = "abcdfeghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	if s.posix {
		s.Wordchars += "ßàáâãäåæçèéêëìíîïðñòóôõöøùúûüýþÿÀÁÂÃÄÅÆÇÈÉÊËÌÍÎÏÐÑÒÓÔÕÖØÙÚÛÜÝÞ"
	}
	s.whitespace = " \t\r\n"
	s.whitespace_split = false
	s.Quotes = "'\""
	s.escape = "\\"
	s.escapedquotes = "\""
	s.state = new(string)
	*s.state = " "
	s.pushback = list.List{}
	s.lineno = 1
	s.debug = 0
	s.token = ""
	s.filestack = list.List{}
	s.source = ""
	s.punctuation_chars = punctuation_chars
	if punctuation_chars != "" {
		s._pushback_chars = list.List{}
		s.Wordchars += "~-./*?="
		w := []byte{}
		for _, c := range s.Wordchars {
			in := false
			for _, p := range punctuation_chars {
				if p == c {
					in = true
					break
				}
			}
			if !in {
				w = append(w, byte(c))
			}
		}
		s.Wordchars = string(w)
	}
	return s
}

func DefaultShlex(instream io.Reader) *Shlex {
	return NewShlex(instream, "", false, "")
}

const (
	DefaultCommenters      = "#"
	DefaultWordChars       = "abcdfeghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	DefaultPOSIXWordChars  = "abcdfeghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ßàáâãäåæçèéêëìíîïðñòóôõöøùúûüýþÿÀÁÂÃÄÅÆÇÈÉÊËÌÍÎÏÐÑÒÓÔÕÖØÙÚÛÜÝÞ"
	DefaultWhiteSpace      = " \t\r\n"
	DefualtWhiteSpaceSplit = false
	Quotes                 = "'\""
	Escape                 = "\\"
	EscapedQuotes          = "\""
)

type MyShlexConfig struct {
}

var DefaultMyShlexConfig = MyShlexConfig{}

type MyShlex struct {
	instream io.Reader
}

func (*MyShlex) Check() error {
	return nil
}

func (*MyShlex) Next() (string, error) {
	return "", nil
}

func (*MyShlex) Split() ([]string, error) {
	return nil, nil
}

func NewMyShlex(reader io.Reader, config *MyShlexConfig) *MyShlex {
	s := &MyShlex{instream: reader}

	sConfig := DefaultMyShlexConfig
	if config != nil {
		sConfig = *config
	}

	return s
}
