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

	"github.com/goshuirc/irc-go/ircevent"
)

type empty struct{}

const (
	trustedReadLimit      = 1024 * 1024
	genericTitleReadLimit = 1024 * 32
	titleCharLimit        = 400

	concurrencyLimit = 128

	IRCv3TimestampFormat = "2006-01-02T15:04:05.000Z"

	fakeUA = "Mozilla/5.0 (Windows NT 6.1; Win64; x64; rv:47.0) Gecko/20100101 Firefox/47.0"

	replyTagName = "+draft/reply"
)

var (
	// drop NUL and \r, misc space characters -> " ", \n -> "  "
	normalizeReplacer = strings.NewReplacer("\x00", "", "\r", "", "\t", " ", "\v", " ", "\f", " ", "\u0085", " ", "\u00a0", " ", "\n", "  ")

	urlRe   = regexp.MustCompile(`(?i)(https?://.*?)( |$)`)
	tweetRe = regexp.MustCompile(`(?i)https://(mobile\.?)?twitter.com/.*/status/([0-9]+)`)
	// <title>bar</title>, <title data-react-helmet="true">qux</title>
	genericTitleRe = regexp.MustCompile(`(?is)<\s*title\b.*?>(.*?)<`)
	youtubeTitleRe = regexp.MustCompile(`(?is)<meta name="title" content="(.*?)"`)

	httpClient = &http.Client{
		Timeout: 15 * time.Second,
	}
)

// XXX make a message safe for IRC; we mainly need to kill NUL, \r, and \n
func normalizeMessage(message string) string {
	return normalizeReplacer.Replace(message)
}

type Bot struct {
	ircevent.Connection
	TwitterBearerToken string
	Owner              string
	semaphore          chan empty
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

func findURL(str string) (url string) {
	m := urlRe.FindStringSubmatch(str)
	if len(m) > 2 {
		return m[1]
	}
	return
}

func extractTweetID(url string) (twid string) {
	tweetMatches := tweetRe.FindStringSubmatch(url)
	if len(tweetMatches) == 3 {
		return tweetMatches[2]
	}
	return
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

	if twid := extractTweetID(url); twid != "" {
		irc.titleTwitter(target, msgid, twid)
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

type TwitterUser struct {
	ID       string
	Username string
	Name     string
	Verified bool
}

type Tweet struct {
	Data struct {
		Text      string
		CreatedAt string `json:"created_at"`
		AuthorID  string `json:"author_id"`
	}
	Includes struct {
		Users []TwitterUser
	}
}

func (irc *Bot) titleTwitter(target, msgid, twid string) {
	if irc.TwitterBearerToken == "" {
		irc.Log.Printf("set TITLEBOT_TWITTER_BEARER_TOKEN to read tweets\n")
		return
	}
	url := fmt.Sprintf("https://api.twitter.com/2/tweets/%s?tweet.fields=created_at&expansions=author_id&user.fields=verified", twid)
	req, err := http.NewRequest("GET", url, nil)
	if irc.checkErr(err, "NewRequest error in titleTwitter") {
		return
	}
	headers := map[string][]string{
		"Authorization": {fmt.Sprintf("Bearer %s", irc.TwitterBearerToken)},
	}
	req.Header = headers
	resp, err := httpClient.Do(req)
	if irc.checkErr(err, "http error in titleTwitter") {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		irc.Log.Printf("bad http code in titleTwitter: %d\n", resp.StatusCode)
		return
	}
	br := io.LimitedReader{R: resp.Body, N: trustedReadLimit}
	body, err := io.ReadAll(&br)
	if irc.checkErr(err, "error reading tweet") {
		return
	}
	var tweet Tweet
	err = json.Unmarshal(body, &tweet)
	if irc.checkErr(err, "error deserializing tweet") {
		return
	}
	var author string
	var verified bool
	for _, incl := range tweet.Includes.Users {
		if incl.ID == tweet.Data.AuthorID {
			author = incl.Username
			verified = incl.Verified
			break
		}
	}
	ts, err := time.Parse(IRCv3TimestampFormat, tweet.Data.CreatedAt)
	if irc.checkErr(err, "invalid time created in tweet") {
		return
	}
	maybeCheckmark := ""
	if verified {
		maybeCheckmark = " \u2713" // 'CHECK MARK' (U+2713)
	}
	timeStr := displayTwitterTime(ts)
	// https://stackoverflow.com/questions/30704063/the-twitter-api-seems-to-escape-ampersand-but-nothing-else
	safeText := normalizeMessage(html.UnescapeString(tweet.Data.Text))
	message := fmt.Sprintf("(@%s%s, %s) %s", author, maybeCheckmark, timeStr, safeText)
	irc.sendReplyNotice(target, msgid, message)
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
		"User-Agent": {fakeUA},
	}
	req.Header = headers

	resp, err := httpClient.Do(req)
	if irc.checkErr(err, "http error in titleGeneric") {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	br := io.LimitedReader{R: resp.Body, N: int64(byteLimit)}
	body, err := io.ReadAll(&br)
	// ErrUnexpectedEOF is OK if we didn't get the whole page
	if !(err == nil || err == io.ErrUnexpectedEOF) {
		irc.Log.Printf("couldn't read in titleGeneric: %v\n", err)
		return
	}
	titleMatch := titleRe.FindSubmatch(body)
	if len(titleMatch) == 2 {
		title := string(titleMatch[1])
		title = html.UnescapeString(title)
		title = strings.TrimSpace(title)
		title = normalizeMessage(title)
		if len(title) > titleCharLimit {
			title = title[:titleCharLimit]
		}
		if len(title) != 0 {
			irc.sendReplyNotice(target, msgid, title)
		}
	}
}

func domainMatch(host, domain string) bool {
	host = strings.ToLower(host)
	trimmed := strings.TrimSuffix(host, domain)
	return host != trimmed && (trimmed == "" || strings.HasSuffix(trimmed, "."))
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
	if domainMatch(host, "youtube.com") || domainMatch(host, "youtu.be") {
		// with youtube we have to check for the <meta> tag instead of <title>
		return trustedReadLimit, youtubeTitleRe, nil
	} else if domainMatch(host, "amazon.com") || domainMatch(host, "amazon.ca") || domainMatch(host, "amzn.to") {
		// amazon sends a bunch of nonsense JS before the <title> tag
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

func ownerMatches(e ircevent.Event, owner string) bool {
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
	// a Twitter API key (v2-capable) is optional (if unset, Twitter support is disabled):
	twitterToken := os.Getenv("TITLEBOT_TWITTER_BEARER_TOKEN")
	// owner is optional (if unset, titlebot won't accept any owner commands)
	owner := os.Getenv("TITLEBOT_OWNER_ACCOUNT")
	// more optional settings
	version := os.Getenv("TITLEBOT_VERSION")
	if version == "" {
		version = "github.com/goshuirc/irc-go"
	}
	debug := os.Getenv("TITLEBOT_DEBUG") != ""
	insecure := os.Getenv("TITLEBOT_INSECURE_SKIP_VERIFY") != ""

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
		TwitterBearerToken: twitterToken,
		Owner:              owner,
		semaphore:          make(chan empty, concurrencyLimit),
	}

	irc.AddConnectCallback(func(e ircevent.Event) {
		if botMode := irc.ISupport()["BOT"]; botMode != "" {
			irc.Send("MODE", irc.CurrentNick(), "+"+botMode)
		}
		for _, channel := range strings.Split(channels, ",") {
			irc.Join(strings.TrimSpace(channel))
		}
	})
	irc.AddCallback("PRIVMSG", func(e ircevent.Event) {
		target, message := e.Params[0], e.Params[1]
		_, msgid := e.GetTag("msgid")
		fromOwner := ownerMatches(e, irc.Owner)
		if !strings.HasPrefix(target, "#") && !fromOwner {
			return
		}
		if url := findURL(message); url != "" {
			go irc.title(e.Params[0], msgid, url)
		}
		if fromOwner {
			irc.handleOwnerCommand(e.Params[0], message)
		} else if strings.HasPrefix(message, irc.Nick) {
			irc.sendReplyNotice(e.Params[0], msgid, "don't @ me, fleshbag")
		}
	})
	irc.AddCallback("INVITE", func(e ircevent.Event) {
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