package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip10"
	"github.com/nbd-wtf/go-nostr/nip19"
)

type EnhancedEvent struct {
	event  *nostr.Event
	relays []string
}

func (ee EnhancedEvent) IsReply() bool {
	return nip10.GetImmediateReply(ee.event.Tags) != nil
}

func (ee EnhancedEvent) Preview() template.HTML {
	lines := strings.Split(html.EscapeString(ee.event.Content), "\n")
	var processedLines []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		processedLine := shortenNostrURLs(line)
		processedLines = append(processedLines, processedLine)
	}

	return template.HTML(strings.Join(processedLines, "<br/>"))
}

func (ee EnhancedEvent) Npub() string {
	npub, _ := nip19.EncodePublicKey(ee.event.PubKey)
	return npub
}

func (ee EnhancedEvent) NpubShort() string {
	npub := ee.Npub()
	return npub[:8] + "…" + npub[len(npub)-4:]
}

func (ee EnhancedEvent) Nevent() string {
	nevent, _ := nip19.EncodeEvent(ee.event.ID, ee.relays, ee.event.PubKey)
	return nevent
}

func (ee EnhancedEvent) CreatedAtStr() string {
	return time.Unix(int64(ee.event.CreatedAt), 0).Format("2006-01-02 15:04:05")
}

func (ee EnhancedEvent) ModifiedAtStr() string {
	return time.Unix(int64(ee.event.CreatedAt), 0).Format("2006-01-02T15:04:05Z07:00")
}

type Data struct {
	templateId          TemplateID
	event               *nostr.Event
	relays              []string
	npub                string
	npubShort           string
	nprofile            string
	nevent              string
	neventNaked         string
	naddr               string
	naddrNaked          string
	createdAt           string
	modifiedAt          string
	parentLink          template.HTML
	metadata            nostr.ProfileMetadata
	authorRelays        []string
	authorLong          string
	authorShort         string
	renderableLastNotes []EnhancedEvent
	kindDescription     string
	kindNIP             string
	video               string
	videoType           string
	image               string
	content             string
	kind1063Metadata    map[string]string
}

func grabData(ctx context.Context, code string, isProfileSitemap bool) (*Data, error) {
	// code can be a nevent, nprofile, npub or nip05 identifier, in which case we try to fetch the associated event
	event, relays, err := getEvent(ctx, code, nil)
	if err != nil {
		log.Warn().Err(err).Str("code", code).Msg("failed to fetch event for code")
		return nil, err
	}

	relaysForNip19 := make([]string, 0, 3)
	for i, relay := range relays {
		relaysForNip19 = append(relaysForNip19, relay)
		if i == 2 {
			break
		}
	}

	npub, _ := nip19.EncodePublicKey(event.PubKey)
	nprofile := ""
	nevent, _ := nip19.EncodeEvent(event.ID, relaysForNip19, event.PubKey)
	neventNaked, _ := nip19.EncodeEvent(event.ID, nil, event.PubKey)
	naddr := ""
	naddrNaked := ""
	createdAt := time.Unix(int64(event.CreatedAt), 0).Format("2006-01-02 15:04:05")
	modifiedAt := time.Unix(int64(event.CreatedAt), 0).Format("2006-01-02T15:04:05Z07:00")

	author := event
	var renderableLastNotes []EnhancedEvent
	var parentLink template.HTML
	authorRelays := []string{}
	var content string
	var templateId TemplateID
	var kind1063Metadata map[string]string

	eventRelays := []string{}
	for _, relay := range relays {
		for _, excluded := range excludedRelays {
			if strings.Contains(relay, excluded) {
				continue
			}
		}
		if strings.Contains(relay, "/npub1") {
			continue // skip relays with personalyzed query like filter.nostr.wine
		}
		eventRelays = append(eventRelays, trimProtocol(relay))
	}

	switch event.Kind {
	case 0:
		{
			rawAuthorRelays := []string{}
			ctx, cancel := context.WithTimeout(ctx, time.Second*4)
			rawAuthorRelays = relaysForPubkey(ctx, event.PubKey)
			cancel()
			for _, relay := range rawAuthorRelays {
				for _, excluded := range excludedRelays {
					if strings.Contains(relay, excluded) {
						continue
					}
				}
				if strings.Contains(relay, "/npub1") {
					continue // skip relays with personalyzed query like filter.nostr.wine
				}
				authorRelays = append(authorRelays, trimProtocol(relay))
			}
		}

		lastNotes := authorLastNotes(ctx, event.PubKey, authorRelays, isProfileSitemap)
		renderableLastNotes = make([]EnhancedEvent, len(lastNotes))
		for i, levt := range lastNotes {
			renderableLastNotes[i] = EnhancedEvent{levt, []string{}}
		}
		if err != nil {
			return nil, err
		}
	case 1, 7, 30023, 30024:
		templateId = Note
		content = event.Content
		if parentNevent := getParentNevent(event); parentNevent != "" {
			parentLink = template.HTML(replaceNostrURLsWithTags(nostrNoteNeventMatcher, "nostr:"+parentNevent))
		}
	case 6:
		templateId = Note
		if reposted := event.Tags.GetFirst([]string{"e", ""}); reposted != nil {
			original_nevent, _ := nip19.EncodeEvent((*reposted)[1], []string{}, "")
			content = "Repost of nostr:" + original_nevent
		}
	case 1063:
		templateId = FileMetadata
		kind1063Metadata = make(map[string]string)

		keysToExtract := []string{
			"url",
			"m",
			"aes-256-gcm",
			"x",
			"size",
			"dim",
			"magnet",
			"i",
			"blurhash",
			"thumb",
			"image",
			"summary",
			"alt",
		}

		for _, tag := range event.Tags {
			if len(tag) == 2 {
				key := tag[0]
				value := tag[1]

				// Check if the key is in the list of keys to extract
				for _, k := range keysToExtract {
					if key == k {
						kind1063Metadata[key] = value
						break
					}
				}
			}
		}

	default:
		if event.Kind >= 30000 && event.Kind < 40000 {
			templateId = Other
			if d := event.Tags.GetFirst([]string{"d", ""}); d != nil {
				naddr, _ = nip19.EncodeEntity(event.PubKey, event.Kind, d.Value(), relaysForNip19)
				naddrNaked, _ = nip19.EncodeEntity(event.PubKey, event.Kind, d.Value(), nil)
			}
		}
	}

	if event.Kind != 0 {
		ctx, cancel := context.WithTimeout(ctx, time.Second*3)
		author, relays, _ = getEvent(ctx, npub, relaysForNip19)
		if len(relays) > 0 {
			nprofile, _ = nip19.EncodeProfile(event.PubKey, limitAt(relays, 2))
		}
		cancel()
	}

	kindDescription := kindNames[event.Kind]
	if kindDescription == "" {
		kindDescription = fmt.Sprintf("Kind %d", event.Kind)
	}
	kindNIP := kindNIPs[event.Kind]

	var image string
	var video string
	var videoType string
	if event.Kind == 1063 {
		if strings.HasPrefix(kind1063Metadata["m"], "image") {
			image = kind1063Metadata["url"]
		} else if strings.HasPrefix(kind1063Metadata["m"], "video") {
			video = kind1063Metadata["url"]
			videoType = strings.Split(kind1063Metadata["m"], "/")[1]
		}
	} else {
		urls := urlMatcher.FindAllString(event.Content, -1)
		for _, url := range urls {
			switch {
			case imageExtensionMatcher.MatchString(url):
				if image == "" {
					image = url
				}
			case videoExtensionMatcher.MatchString(url):
				if video == "" {
					video = url
					if strings.HasSuffix(video, "mp4") {
						videoType = "mp4"
					} else if strings.HasSuffix(video, "mov") {
						videoType = "mov"
					} else {
						videoType = "webm"
					}
				}
			}
		}
	}

	npubShort := npub[:8] + "…" + npub[len(npub)-4:]
	authorLong := npub
	authorShort := npubShort

	var metadata nostr.ProfileMetadata
	if author != nil {
		if err := json.Unmarshal([]byte(author.Content), &metadata); err == nil {
			authorLong = fmt.Sprintf("%s (%s)", metadata.Name, npub)
			authorShort = fmt.Sprintf("%s (%s)", metadata.Name, npubShort)
		}
	}

	return &Data{
		templateId:          templateId,
		event:               event,
		relays:              eventRelays,
		npub:                npub,
		npubShort:           npubShort,
		nprofile:            nprofile,
		nevent:              nevent,
		neventNaked:         neventNaked,
		naddr:               naddr,
		naddrNaked:          naddrNaked,
		authorRelays:        authorRelays,
		createdAt:           createdAt,
		modifiedAt:          modifiedAt,
		parentLink:          parentLink,
		metadata:            metadata,
		authorLong:          authorLong,
		authorShort:         authorShort,
		renderableLastNotes: renderableLastNotes,
		kindNIP:             kindNIP,
		kindDescription:     kindDescription,
		video:               video,
		videoType:           videoType,
		image:               image,
		content:             content,
		kind1063Metadata:    kind1063Metadata,
	}, nil
}
