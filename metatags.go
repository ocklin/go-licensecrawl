package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"io"

	"golang.org/x/net/html"
)

type HTMLMeta struct {
	Sitename  string `json:"sitename"`
	GitSource string `json:"gitsource"`
	GitImport string `json:"gitimport"`
}

func getMetaTags(link string) {

	resp, err := http.Get(link)
	if err != nil {
		fmt.Printf("Cant open link %s: %s\n", link, err)
		return
	}
	defer resp.Body.Close()

	meta := extract(resp.Body)

	j, _ := json.MarshalIndent(meta, "", "  ")

	fmt.Println(string(j))
}

func extract(resp io.Reader) *HTMLMeta {
	z := html.NewTokenizer(resp)

	hm := new(HTMLMeta)

	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return hm
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			if t.Data == `body` {
				return hm
			}

			// <meta name=go-import content="go.my.org/myproj git https://github.com/my/myproj.git">
			// <meta name="go-source"
			//   content="go.my.org/myproj
			//			https://github.com/my/myproj
			//			https://github.com/my/myproj/tree/master{/dir}
			//			https://github.com/github.com/my/myproj/blob/master{/dir}/{file}#L{line}">

			if t.Data == "meta" {
				//printMetaProperty(t)
				desc, ok := extractMetaProperty(t, "go-import")
				if ok {
					fmt.Printf("go-import: %s\n", desc)
					hm.GitImport = desc
				}
				desc, ok = extractMetaProperty(t, "go-source")
				if ok {
					fmt.Printf("go-source: %s\n", desc)
					hm.GitSource = desc
				}

			}
		}
	}
}

func extractMetaProperty(t html.Token, prop string) (content string, ok bool) {
	for _, attr := range t.Attr {
		if attr.Key == "name" && attr.Val == prop {
			ok = true
		}

		if attr.Key == "content" {
			content = attr.Val
		}
	}

	return content, ok
}

func printMetaProperty(t html.Token) {
	for _, attr := range t.Attr {
		fmt.Println("key: ", attr.Key, ", value: ", attr.Val)
	}

	return
}
