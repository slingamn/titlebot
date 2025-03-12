// Copyright (c) 2021 Shivaram Lingamneni
// Released under the MIT License

package main

// titlebot is a simple bot that downloads linked webpages, extracts their
// titles, and sends them to the channel as a NOTICE. It can also read
// Tweets. It is configured via environment variables (see newBot for a list).

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/ergochat/irc-go/ircevent"
	"github.com/ergochat/irc-go/ircmsg"
	"github.com/ergochat/irc-go/ircutils"
)

type empty struct{}

const (
	trustedReadLimit      = 1024 * 1024
	genericTitleReadLimit = 1024 * 64
	titleCharLimit        = 400
	maxUrlsPerMessage     = 4

	concurrencyLimit = 128

	IRCv3TimestampFormat = "2006-01-02T15:04:05.000Z"

	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.81 Safari/537.36"

	replyTagName = "+draft/reply"

	blueskyAPIBase = "https://bsky.social/xrpc"
)

var (
	urlRe     = regexp.MustCompile(`\b(?i)(https?://.*?)(\s|$)`)
	blueskyRe = regexp.MustCompile(`https://bsky\.app/profile/([^/]+)/post/([^/]+)`)
	// <title>bar</title>, <title data-react-helmet="true">qux</title>
	genericTitleRe = regexp.MustCompile(`(?is)<\s*title\b.*?>(.*?)<`)

	httpClient = &http.Client{
		Timeout: 15 * time.Second,
	}
)

type Bot struct {
	ircevent.Connection
	Owner              string
	semaphore          chan empty
	userAgent          string
}

func (b *Bot) tryAcquireSemaphore() bool {
	select {
	case b.semaphore <- empty{}:
		return true
	default:
		return false
	}
}

func (b *Bot) releaseSemaphore() {
	<-b.semaphore
}

func findURL(str string) (urls []string) {
	matches := urlRe.FindAllStringSubmatch(str, -1)
	if matches == nil {
		return
	}
	urls = make([]string, 0, len(matches))
	for _, submatch := range matches {
		if len(submatch) > 2 {
			urls = append(urls, submatch[1])
		}
	}
	return
}

func extractBlueskyData(url string) (handle, postid string) {
	matches := blueskyRe.FindStringSubmatch(url)
	if len(matches) == 3 {
		return matches[1], matches[2]
	}
	return
}

func (irc *Bot) titleAll(target, msgid string, urls []string) {
	if len(urls) > maxUrlsPerMessage {
		urls = urls[:maxUrlsPerMessage]
	}
	for _, url := range urls {
		irc.title(target, msgid, url)
	}
}

func (irc *Bot) title(target, msgid, url string) {
	if !irc.tryAcquireSemaphore() {
		irc.Log.Printf("concurrency limit exceeded, not titling %s\n", url)
		return
	}
	defer irc.releaseSemaphore()

	defer func() {
		if r := recover(); r != nil {
			irc.Log.Printf("Caught panic in callback: %v\n%s", r, debug.Stack())
		}
	}()

	start := time.Now()
	defer func() {
		if irc.Debug {
			irc.Log.Printf("Titled %s in %v\n", url, time.Since(start))
		}
	}()

	if handle, postid := extractBlueskyData(url); handle != "" {
		irc.titleBluesky(target, msgid, handle, postid)
	} else {
		irc.titleGeneric(target, msgid, url)
	}
}

func (irc *Bot) checkErr(err error, message string) (fatal bool) {
	if err != nil {
		irc.Log.Printf("%s: %v", message, err)
		return true
	}
	return false
}

type BlueskyPostRecord struct {
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
	//Embed     *Embed    `json:"embed,omitempty"`
	//Facets    []Facet   `json:"facets,omitempty"`
	//Reply     *Reply    `json:"reply,omitempty"`
	//Langs     []string  `json:"langs,omitempty"`
}

func (irc *Bot) titleBluesky(target, msgid, handle, postid string) {
	did := irc.resolveBlueskyHandle(handle)
	if did == "" {
		return
	}
	record, err := irc.getBlueskyPost(did, postid)
	if err != nil {
		return
	}

	ts, err := time.Parse(IRCv3TimestampFormat, record.CreatedAt)
	if irc.checkErr(err, "invalid time created in bsky post") {
		return
	}
	timeStr := displayTwitterTime(ts)
	safeText := ircutils.SanitizeText(record.Text, titleCharLimit)
	message := fmt.Sprintf("(@%s, %s) %s", handle, timeStr, safeText)
	irc.sendReplyNotice(target, msgid, message)
}

func (irc *Bot) resolveBlueskyHandle(handle string) (did string) {
	url := fmt.Sprintf("%s/com.atproto.identity.resolveHandle?handle=%s", blueskyAPIBase, handle)

	resp, err := httpClient.Get(url)
	if irc.checkErr(err, "can't get resolve bluesky handle") {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		irc.Log.Printf("Can't resolve bsky handle %s : HTTP code %d\n", handle, resp.StatusCode)
		return
	}

	br := io.LimitedReader{R: resp.Body, N: trustedReadLimit}
	type ResolveHandleResponse struct {
		Did string `json:"did"`
	}
	var result ResolveHandleResponse
	if irc.checkErr(json.NewDecoder(&br).Decode(&result), "Invalid JSON from bsky handle resolve") {
		return
	}
	return result.Did
}

func (irc *Bot) getBlueskyPost(did, postID string) (record BlueskyPostRecord, err error) {
	url := fmt.Sprintf("%s/com.atproto.repo.getRecord?repo=%s&collection=app.bsky.feed.post&rkey=%s",
		blueskyAPIBase, did, postID)

	resp, err := httpClient.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		irc.Log.Printf("Can't resolve bsky handle %s : HTTP code %d\n", did, resp.StatusCode)
		return
	}

	br := io.LimitedReader{R: resp.Body, N: trustedReadLimit}
	type GetRecordResponse struct {
		URI   string            `json:"uri"`
		CID   string            `json:"cid"`
		Value BlueskyPostRecord `json:"value"`
	}
	var result GetRecordResponse
	if irc.checkErr(json.NewDecoder(&br).Decode(&result), "Invalid JSON from bsky post lookup") {
		irc.Log.Printf("Invalid JSON from bsky getRecord: %v", err)
		return record, err
	}

	return result.Value, nil
}

func displayTwitterTime(then time.Time) string {
	elapsed := time.Since(then)
	if elapsed > 7*24*time.Hour {
		return then.Format("2006-01-02")
	} else {
		return humanReadableDuration(elapsed) + " ago"
	}
}

var humanDurations = []struct {
	dur  time.Duration
	name string
}{
	{dur: time.Hour * 24 * 365, name: "y"},
	{dur: time.Hour * 24, name: "d"},
	{dur: time.Hour, name: "h"},
	{dur: time.Minute, name: "m"},
	{dur: time.Second, name: "s"},
	{dur: time.Millisecond, name: "ms"},
}

func humanReadableDuration(d time.Duration) string {
	var out strings.Builder

	found := -1
	for i, up := range humanDurations {
		if found != -1 && i > found+1 {
			break
		}
		if d > up.dur {
			count := d / up.dur
			fmt.Fprintf(&out, "%d%s", count, up.name)
			if found == -1 {
				found = i
			}
			d = d % up.dur
		}
	}

	return out.String()
}

func (irc *Bot) titleGeneric(target, msgid, url string) {
	byteLimit, titleRe, err := irc.analyzeURL(url)
	if irc.checkErr(err, "invalid URL") {
		return
	}
	req, err := http.NewRequest("GET", url, nil)
	if irc.checkErr(err, "NewRequest error in titleTwitter") {
		return
	}
	headers := map[string][]string{
		"User-Agent": {irc.userAgent},
	}
	req.Header = headers

	resp, err := httpClient.Do(req)
	if irc.checkErr(err, "http error in titleGeneric") {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if irc.Debug {
			irc.Log.Printf("Can't title %s : HTTP code %d\n", url, resp.StatusCode)
		}
		return
	}
	br := io.LimitedReader{R: resp.Body, N: int64(byteLimit)}
	body, err := io.ReadAll(&br)
	// ErrUnexpectedEOF is OK if we didn't get the whole page
	if !(err == nil || err == io.ErrUnexpectedEOF) {
		irc.Log.Printf("couldn't read in titleGeneric: %v\n", err)
		return
	}
	success := false
	titleMatch := titleRe.FindSubmatch(body)
	if len(titleMatch) == 2 {
		title := string(titleMatch[1])
		title = html.UnescapeString(title)
		title = strings.TrimSpace(title)
		title = ircutils.SanitizeText(title, titleCharLimit)
		if len(title) != 0 {
			success = true
			irc.sendReplyNotice(target, msgid, title)
		}
	}
	if !success && irc.Debug {
		irc.Log.Printf("Can't title %s : title not found\n", url)
	}
}

func domainMatch(host, domain string) bool {
	// XXX host must already be lowercase
	trimmed := strings.TrimSuffix(host, domain)
	return host != trimmed && (trimmed == "" || strings.HasSuffix(trimmed, "."))
}

// these domains send a lot of garbage JS ahead of the title tag,
// so we need to extend the read limit to handle them
var garbageJSDomains = []string{
	"amazon.com",
	"amazon.ca",
	"amzn.to",
	"imdb.com",
	"google.com",
	"goo.gl",
	"github.com",
	"youtube.com",
	"youtu.be",
}

func isGarbageJSDomain(host string) bool {
	for _, domain := range garbageJSDomains {
		if domainMatch(host, domain) {
			return true
		}
	}
	return false
}

func (irc *Bot) analyzeURL(urlStr string) (byteLimit int, titleRe *regexp.Regexp, err error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return
	}
	host := u.Host
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	hostLower := strings.ToLower(host)
	if isGarbageJSDomain(hostLower) {
		return trustedReadLimit, genericTitleRe, nil
	} else {
		return genericTitleReadLimit, genericTitleRe, nil
	}
}

func (irc *Bot) handleOwnerCommand(target, command string) {
	if !strings.HasPrefix(command, irc.Nick) {
		return
	}
	command = strings.TrimPrefix(command, irc.Nick)
	command = strings.TrimPrefix(command, ":")
	f := strings.Fields(command)
	if len(f) == 0 {
		return
	}
	switch strings.ToLower(f[0]) {
	case "abuse":
		if len(f) > 1 {
			irc.Privmsg(target, fmt.Sprintf("%s isn't a real programmer", f[1]))
		}
	case "quit":
		irc.Quit()
	}
}

func (irc *Bot) sendReplyNotice(target, msgid, text string) {
	if msgid == "" {
		irc.Notice(target, text)
	} else {
		irc.SendWithTags(map[string]string{replyTagName: msgid}, "NOTICE", target, text)
	}
}

func ownerMatches(e ircmsg.Message, owner string) bool {
	if owner == "" {
		return false
	}
	if present, account := e.GetTag("account"); present && account == owner {
		return true
	}
	return false
}

func newBot() *Bot {
	// required:
	nick := os.Getenv("TITLEBOT_NICK")
	server := os.Getenv("TITLEBOT_SERVER")
	// required (comma-delimited list of channels)
	channels := os.Getenv("TITLEBOT_CHANNELS")
	// SASL is optional:
	saslLogin := os.Getenv("TITLEBOT_SASL_LOGIN")
	saslPassword := os.Getenv("TITLEBOT_SASL_PASSWORD")
	// owner is optional (if unset, titlebot won't accept any owner commands)
	owner := os.Getenv("TITLEBOT_OWNER_ACCOUNT")
	// more optional settings
	version := os.Getenv("TITLEBOT_VERSION")
	if version == "" {
		version = "github.com/ergochat/irc-go"
	}
	debug := os.Getenv("TITLEBOT_DEBUG") != ""
	insecure := os.Getenv("TITLEBOT_INSECURE_SKIP_VERIFY") != ""
	userAgent := os.Getenv("TITLEBOT_USER_AGENT")
	if userAgent == "" {
		userAgent = defaultUserAgent
	}

	var tlsconf *tls.Config
	if insecure {
		tlsconf = &tls.Config{InsecureSkipVerify: true}
	}

	irc := &Bot{
		Connection: ircevent.Connection{
			Server:       server,
			Nick:         nick,
			UseTLS:       true,
			TLSConfig:    tlsconf,
			RequestCaps:  []string{"server-time", "message-tags", "account-tag"},
			SASLLogin:    saslLogin, // SASL will be enabled automatically if these are set
			SASLPassword: saslPassword,
			QuitMessage:  version,
			Debug:        debug,
		},
		Owner:              owner,
		userAgent:          userAgent,
		semaphore:          make(chan empty, concurrencyLimit),
	}

	irc.AddConnectCallback(func(e ircmsg.Message) {
		if botMode := irc.ISupport()["BOT"]; botMode != "" {
			irc.Send("MODE", irc.CurrentNick(), "+"+botMode)
		}
		for _, channel := range strings.Split(channels, ",") {
			irc.Join(strings.TrimSpace(channel))
		}
	})
	irc.AddCallback("PRIVMSG", func(e ircmsg.Message) {
		target, message := e.Params[0], e.Params[1]
		_, msgid := e.GetTag("msgid")
		fromOwner := ownerMatches(e, irc.Owner)
		if !strings.HasPrefix(target, "#") && !fromOwner {
			return
		}
		if urls := findURL(message); urls != nil {
			go irc.titleAll(e.Params[0], msgid, urls)
		}
		if fromOwner {
			irc.handleOwnerCommand(e.Params[0], message)
		} else if strings.HasPrefix(message, irc.Nick) {
			irc.sendReplyNotice(e.Params[0], msgid, "don't @ me, mortal")
		}
	})
	irc.AddCallback("INVITE", func(e ircmsg.Message) {
		fromOwner := ownerMatches(e, irc.Owner)
		if fromOwner {
			irc.Join(e.Params[1])
		}
	})

	return irc
}

func main() {
	irc := newBot()
	err := irc.Connect()
	if err != nil {
		log.Fatal(err)
	}
	irc.Loop()
}
