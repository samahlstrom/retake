package linkcard

import (
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// How long each of a card's fields may be, in characters.
const (
	maxTitle       = 300
	maxSite        = 100
	maxImage       = 2048
	maxDescription = 500
)

// Parse reads a page's head (spec/api.md, "Link preview"): the og: tags, then twitter: ones, then <title> and
// <meta name="description">. It stops at <body> or </head>. base is the page's address, for a relative og:image;
// only an https picture is kept.
func Parse(body io.Reader, base *url.URL) Card {
	meta := map[string]string{}
	var title strings.Builder
	inTitle, gotTitle := false, false
	z := html.NewTokenizer(body)
	z.SetMaxBuf(MaxBody)
loop:
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			break loop
		case html.TextToken:
			if inTitle && !gotTitle {
				title.Write(z.Text())
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch atom.Lookup(name) {
			case atom.Title:
				if inTitle {
					inTitle, gotTitle = false, true
				}
			case atom.Head:
				break loop
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch atom.Lookup(name) {
			case atom.Body:
				break loop
			case atom.Title:
				inTitle = !gotTitle && tt == html.StartTagToken
			case atom.Meta:
				var key, content string
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					switch string(k) {
					case "property", "name":
						if key == "" {
							key = strings.ToLower(strings.TrimSpace(string(v)))
						}
					case "content":
						content = string(v)
					}
				}
				if _, seen := meta[key]; key != "" && !seen {
					meta[key] = content
				}
			}
		}
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := clean(meta[k]); v != "" {
				return v
			}
		}
		return ""
	}
	c := Card{
		Title:       cut(first("og:title", "twitter:title"), maxTitle),
		Site:        cut(first("og:site_name"), maxSite),
		Description: cut(first("og:description", "twitter:description", "description"), maxDescription),
	}
	if c.Title == "" {
		c.Title = cut(clean(title.String()), maxTitle)
	}
	if img := first("og:image:secure_url", "og:image", "og:image:url", "twitter:image", "twitter:image:src"); img != "" {
		if u, err := base.Parse(img); err == nil && u.Scheme == "https" && u.Host != "" && len(u.String()) <= maxImage {
			c.Image = u.String()
		}
	}
	return c
}

// clean is text made one line: valid UTF-8, its runs of spaces one space, trimmed. The tokenizer already decoded
// its entities.
func clean(s string) string {
	return strings.Join(strings.Fields(strings.ToValidUTF8(s, "")), " ")
}

// cut is s at most n characters long, with … when it was longer.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
