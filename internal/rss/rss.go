package rss

import (
	"encoding/xml"
	"fmt"
	"net/url"
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

// ItemOptions control enclosure and chapter URL construction.
type ItemOptions struct {
	// BaseURL is PUBLIC_BASE_URL without a trailing slash.
	BaseURL string
	// Token, if set, is appended to enclosure and chapter URLs as ?token=.
	Token string
	// AudioPath is the enclosure directory, default "/audio".
	AudioPath string
	// ChaptersPath is the chapters directory, default "/episodes".
	ChaptersPath string
	// CoverURL is the fallback artwork URL for episodes without a custom ImageURL.
	CoverURL string
}

// Options is an alias for ItemOptions.
type Options = ItemOptions

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	ITunes  string   `xml:"xmlns:itunes,attr"`
	Atom    string   `xml:"xmlns:atom,attr"`
	Content string   `xml:"xmlns:content,attr"`
	PSC     string   `xml:"xmlns:psc,attr"`
	Podcast string   `xml:"xmlns:podcast,attr"`
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

type itunesImage = itImage

type itOwner struct {
	Name  string `xml:"itunes:name"`
	Email string `xml:"itunes:email"`
}

type pscChapters struct {
	Version  string       `xml:"version,attr"`
	Chapters []pscChapter `xml:"psc:chapter"`
}

type pscChapter struct {
	Start string `xml:"start,attr"`
	Title string `xml:"title,attr"`
	Href  string `xml:"href,attr,omitempty"`
	Image string `xml:"image,attr,omitempty"`
}

type podcastChapters struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

type item struct {
	Title           string           `xml:"title"`
	Description     string           `xml:"description"`
	Summary         string           `xml:"itunes:summary,omitempty"`
	ContentEncoded  string           `xml:"content:encoded,omitempty"`
	PubDate         string           `xml:"pubDate"`
	GUID            guid             `xml:"guid"`
	Enclosure       enclosure        `xml:"enclosure"`
	Duration        string           `xml:"itunes:duration"`
	Explicit        string           `xml:"itunes:explicit"`
	Author          string           `xml:"itunes:author,omitempty"`
	Category        string           `xml:"category,omitempty"`
	Image           *itunesImage     `xml:"itunes:image,omitempty"`
	PSCChapters     *pscChapters     `xml:"psc:chapters,omitempty"`
	PodcastChapters *podcastChapters `xml:"podcast:chapters,omitempty"`
}

type rssItem = item

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
		switch {
		case strings.Contains(ct, "wav"):
			ext = "wav"
		case strings.Contains(ct, "aac"):
			ext = "aac"
		}
		audioPath := opt.AudioPath
		if audioPath == "" {
			audioPath = "/audio"
		}
		base := strings.TrimRight(opt.BaseURL, "/")
		encURL := base + strings.TrimRight(audioPath, "/") + "/" + ep.ID + "." + ext
		if opt.Token != "" {
			encURL += "?token=" + url.QueryEscape(opt.Token)
		}
		baseText := strings.TrimSpace(ep.Description)
		if baseText == "" {
			baseText = strings.TrimSpace(ep.ScriptText)
		}
		if baseText == "" {
			baseText = ep.Title
		}
		desc := baseText
		if ep.Category != "" && strings.TrimSpace(ep.Description) == "" {
			desc = ep.Category + ": " + baseText
		}
		if len(ep.Chapters) > 0 {
			var b strings.Builder
			b.WriteString(desc)
			b.WriteString("\n\nChapters:")
			for _, chp := range ep.Chapters {
				b.WriteString("\n")
				b.WriteString(formatDuration(chp.StartSeconds))
				b.WriteString(" ")
				b.WriteString(chp.Title)
			}
			desc = b.String()
		}
		var itemImage *itunesImage
		if ep.ImageURL != "" {
			itemImage = &itunesImage{Href: ep.ImageURL}
		} else if opt.CoverURL != "" {
			itemImage = &itunesImage{Href: opt.CoverURL}
		} else if ch.ImageURL != "" {
			itemImage = &itunesImage{Href: ch.ImageURL}
		}
		var (
			psc     *pscChapters
			podChap *podcastChapters
		)
		if len(ep.Chapters) > 0 {
			chaptersPath := opt.ChaptersPath
			if chaptersPath == "" {
				chaptersPath = "/episodes"
			}
			chapURL := base + strings.TrimRight(chaptersPath, "/") + "/" + ep.ID + "/chapters.json"
			if opt.Token != "" {
				chapURL += "?token=" + url.QueryEscape(opt.Token)
			}
			podChap = &podcastChapters{
				URL:  chapURL,
				Type: "application/json+chapters",
			}
			pscList := make([]pscChapter, len(ep.Chapters))
			for i, c := range ep.Chapters {
				pscList[i] = pscChapter{
					Start: formatChapterStart(c.StartSeconds),
					Title: c.Title,
					Href:  c.URL,
					Image: c.ImageURL,
				}
			}
			psc = &pscChapters{
				Version:  "1.2",
				Chapters: pscList,
			}
		}
		items = append(items, item{
			Title:          ep.Title,
			Description:    desc,
			Summary:        desc,
			ContentEncoded: desc,
			PubDate:        pub.UTC().Format(time.RFC1123Z),
			GUID:           guid{IsPermaLink: "false", Value: ep.ID},
			Enclosure: enclosure{
				URL:    encURL,
				Length: fmt.Sprintf("%d", ep.FileSizeBytes),
				Type:   ct,
			},
			Duration:        formatDuration(ep.DurationSeconds),
			Explicit:        boolStr(ch.Explicit),
			Author:          ch.Author,
			Category:        ep.Category,
			Image:           itemImage,
			PSCChapters:     psc,
			PodcastChapters: podChap,
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
	selfURL := ch.FeedURL
	if selfURL == "" {
		selfURL = strings.TrimRight(ch.Link, "/") + "/podcast.xml"
	}
	if opt.Token != "" && selfURL != "" && !strings.Contains(selfURL, "token=") {
		selfURL += "?token=" + url.QueryEscape(opt.Token)
	}

	doc := rss{
		Version: "2.0",
		ITunes:  "http://www.itunes.com/dtds/podcast-1.0.dtd",
		Atom:    "http://www.w3.org/2005/Atom",
		Content: "http://purl.org/rss/1.0/modules/content/",
		PSC:     "http://podlove.org/simple-chapters",
		Podcast: "https://podcastindex.org/namespace/1.0",
		Channel: channel{
			Title:         ch.Title,
			Link:          ch.Link,
			Description:   ch.Description,
			Language:      lang,
			LastBuildDate: latest.UTC().Format(time.RFC1123Z),
			AtomLink:      atomLink{Href: selfURL, Rel: "self", Type: "application/rss+xml"},
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

func formatChapterStart(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
