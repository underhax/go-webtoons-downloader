package writer

import (
	"encoding/xml"
	"fmt"
	"strconv"
)

type comicInfoXML struct {
	XMLName xml.Name `xml:"ComicInfo"`
	Series  string   `xml:"Series,omitempty"`
	Title   string   `xml:"Title,omitempty"`
	Number  string   `xml:"Number,omitempty"`
	Writer  string   `xml:"Writer,omitempty"`
	Genre   string   `xml:"Genre,omitempty"`
	Summary string   `xml:"Summary,omitempty"`
	Web     string   `xml:"Web,omitempty"`
}

func defaultXMLMarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return xml.MarshalIndent(v, prefix, indent)
}

var xmlMarshalIndent = defaultXMLMarshalIndent

// BuildComicInfoXML marshals chapter metadata into standard ComicInfo.xml bytes.
func BuildComicInfoXML(meta *ChapterMetadata) ([]byte, error) {
	if meta == nil {
		return nil, nil
	}

	numStr := ""
	if meta.ChapterNumber > 0 {
		numStr = strconv.Itoa(meta.ChapterNumber)
	}

	info := comicInfoXML{
		Series:  meta.SeriesTitle,
		Title:   meta.ChapterTitle,
		Number:  numStr,
		Writer:  meta.Author,
		Genre:   meta.Genre,
		Summary: meta.Summary,
		Web:     meta.WebURL,
	}

	data, err := xmlMarshalIndent(info, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal comicinfo xml: %w", err)
	}

	header := []byte("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n")
	return append(header, data...), nil
}
