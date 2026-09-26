package rss

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/tvaroska/podcaster/internal/episode"
)

// Channel describes the podcast-level (show) metadata.
type Channel struct {
	Title       string
	Link        string
	Description string
	Language    string
	Author      string
	OwnerEmail  string
	Category    string
	Explicit    bool
	ImageURL    string
	FeedURL     string
}

// ItemOptions control enclosure URL construction.
type ItemOptions struct {
	// BaseURL is PUBLIC_BASE_URL without a trailing slash.
	BaseURL string
	// Token, if set, is appended to enclosure URLs as ?token=.
	Token string
}

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	ITunes  string   `xml:"xmlns:itunes,attr"`
	Atom    string   `xml:"xmlns:atom,attr"`
	Content string   `xml:"xmlns:content,attr"`
	Channel channel  `xml:"channel"`
}

type channel struct {
	Title         string     `xml:"title"`
	Link          string     `xml:"link"`
	Description   string     `xml:"description"`
	Language      string     `xml:"language"`
	LastBuildDate string     `xml:"lastBuildDate"`
	AtomLink      atomLink   `xml:"atom:link"`
	Author        string     `xml:"itunes:author"`
	Summary       string     `xml:"itunes:summary"`
	Explicit      string     `xml:"itunes:explicit"`
	Block         string     `xml:"itunes:block"`
	Category      itCategory `xml:"itunes:category"`
	Image         itImage    `xml:"itunes:image"`
	Owner         *itOwner   `xml:"itunes:owner,omitempty"`
	Items         []item     `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type itCategory struct {
	Text string `xml:"text,attr"`
}

type itImage struct {
	Href string `xml:"href,attr"`
}

type itOwner struct {
	Name  string `xml:"itunes:name"`
	Email string `xml:"itunes:email"`
}

type item struct {
	Title       string    `xml:"title"`
	Description string    `xml:"description"`
	PubDate     string    `xml:"pubDate"`
	GUID        guid      `xml:"guid"`
	Enclosure   enclosure `xml:"enclosure"`
	Duration    string    `xml:"itunes:duration"`
	Explicit    string    `xml:"itunes:explicit"`
	Author      string    `xml:"itunes:author,omitempty"`
	Category    string    `xml:"category,omitempty"`
}

type guid struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type enclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// Build renders an RSS 2.0 + iTunes podcast feed. Only READY episodes are included.
func Build(ch Channel, episodes []*episode.Episode, opt ItemOptions) ([]byte, error) {
	items := make([]item, 0, len(episodes))
	var latest time.Time
	for _, ep := range episodes {
		if ep.Status != episode.StatusReady {
			continue
		}
		pub := ep.CreatedAt
		if ep.PublishedAt != nil {
			pub = *ep.PublishedAt
		}
		if pub.After(latest) {
			latest = pub
		}
		ct := ep.ContentType
		if ct == "" {
			ct = "audio/mpeg"
		}
		ext := "mp3"
		if strings.Contains(ct, "wav") {
			ext = "wav"
		}
		encURL := strings.TrimRight(opt.BaseURL, "/") + "/audio/" + ep.ID + "." + ext
		if opt.Token != "" {
			encURL += "?token=" + opt.Token
		}
		desc := ep.Title
		if ep.Category != "" {
			desc = ep.Category + ": " + ep.Title
		}
		items = append(items, item{
			Title:       ep.Title,
			Description: desc,
			PubDate:     pub.UTC().Format(time.RFC1123Z),
			GUID:        guid{IsPermaLink: "false", Value: ep.ID},
			Enclosure: enclosure{
				URL:    encURL,
				Length: fmt.Sprintf("%d", ep.FileSizeBytes),
				Type:   ct,
			},
			Duration: formatDuration(ep.DurationSeconds),
			Explicit: boolStr(ch.Explicit),
			Author:   ch.Author,
			Category: ep.Category,
		})
	}
	if latest.IsZero() {
		latest = time.Now().UTC()
	}

	explicit := boolStr(ch.Explicit)
	lang := ch.Language
	if lang == "" {
		lang = "en-us"
	}
	feedURL := ch.FeedURL
	if feedURL == "" {
		feedURL = strings.TrimRight(ch.Link, "/") + "/podcast.xml"
	}

	doc := rss{
		Version: "2.0",
		ITunes:  "http://www.itunes.com/dtds/podcast-1.0.dtd",
		Atom:    "http://www.w3.org/2005/Atom",
		Content: "http://purl.org/rss/1.0/modules/content/",
		Channel: channel{
			Title:         ch.Title,
			Link:          ch.Link,
			Description:   ch.Description,
			Language:      lang,
			LastBuildDate: latest.UTC().Format(time.RFC1123Z),
			AtomLink:      atomLink{Href: feedURL, Rel: "self", Type: "application/rss+xml"},
			Author:        ch.Author,
			Summary:       ch.Description,
			Explicit:      explicit,
			Block:         "yes",
			Category:      itCategory{Text: ch.Category},
			Image:         itImage{Href: ch.ImageURL},
			Items:         items,
		},
	}
	if ch.OwnerEmail != "" {
		doc.Channel.Owner = &itOwner{Name: ch.Author, Email: ch.OwnerEmail}
	}

	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return "0"
	}
	total := int(seconds + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
