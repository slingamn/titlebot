package htmlutil

import (
	"golang.org/x/net/html"
	"io"
	"strings"
)

// Claude wrote this!

// MetaTag represents a meta tag with its attributes
type MetaTag struct {
	Name      string
	Property  string
	Content   string
	HttpEquiv string
	Charset   string
}

// ExtractMetaTags parses HTML content and extracts all meta tags
func ExtractMetaTags(r io.Reader) ([]MetaTag, error) {
	var metaTags []MetaTag

	// Create new tokenizer
	tokenizer := html.NewTokenizer(r)

	for {
		// Get the next token type
		tokenType := tokenizer.Next()

		// Check if we're done or if there's an error
		if tokenType == html.ErrorToken {
			err := tokenizer.Err()
			if err == io.EOF {
				return metaTags, nil
			}
			return metaTags, err
		}

		// We're only interested in start tags
		if tokenType == html.StartTagToken || tokenType == html.SelfClosingTagToken {
			token := tokenizer.Token()

			// We're looking for meta tags
			if token.Data == "meta" {
				var metaTag MetaTag

				// Extract all attributes
				for _, attr := range token.Attr {
					switch strings.ToLower(attr.Key) {
					case "name":
						metaTag.Name = attr.Val
					case "property":
						metaTag.Property = attr.Val
					case "content":
						metaTag.Content = attr.Val
					case "http-equiv":
						metaTag.HttpEquiv = attr.Val
					case "charset":
						metaTag.Charset = attr.Val
					}
				}

				zero := MetaTag{}
				if metaTag != zero {
					metaTags = append(metaTags, metaTag)
				}
			}
		}
	}
}
