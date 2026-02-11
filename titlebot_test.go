package main

import (
	"crypto/rand"
	"regexp"
	"testing"
)

func BenchmarkTitleRegex(b *testing.B) {
	a := make([]byte, genericTitleReadLimit)
	rand.Read(a)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		genericTitleRe.FindSubmatch(a)
	}
}

func BenchmarkActivityPubRegex(b *testing.B) {
	a := make([]byte, genericTitleReadLimit)
	rand.Read(a)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		activityPubRe.Match(a)
	}
}

func TestDomainMatch(t *testing.T) {
	var testCases = []struct {
		host        string
		domain      string
		shouldMatch bool
	}{
		{"www.google.com", "bing.com", false},
		{"www.google.com", "google.com", true},
		{"google.com", "google.com", true},
		{"google.com", "www.google.com", false},
		{"clients3.google.com", "www.google.com", false},
	}

	for _, testCase := range testCases {
		result := domainMatch(testCase.host, testCase.domain)
		if result != testCase.shouldMatch {
			t.Errorf("%s match %s, expected %t, got %t", testCase.host, testCase.domain, testCase.shouldMatch, result)
		}
	}
}

func TestRegexes(t *testing.T) {
	var testCases = []struct {
		desc     string
		input    string
		re       *regexp.Regexp
		expected bool
	}{
		{
			desc:     "activity pub regexp should match alternate link",
			input:    "<head>\n" + `<link href='https://nondeterministic.computer/users/mjg59/statuses/114136289316343772' rel='alternate' type='application/activity+json'>` + "\n</head>",
			re:       activityPubRe,
			expected: true,
		},
		{
			desc:     "activity pub regexp should not match head without link",
			re:       activityPubRe,
			input:    "<head>\n<title>hi</title>\n</head>",
			expected: false,
		},
	}

	for _, testCase := range testCases {
		result := testCase.re.Match([]byte(testCase.input))
		if result != testCase.expected {
			t.Errorf("regex test failed (%s): want %t, got %t", testCase.desc, testCase.expected, result)
		}
	}
}

func TestTweetRe(t *testing.T) {
	var testCases = []struct {
		desc         string
		input        string
		shouldMatch  bool
		expectedUser string
		expectedID   string
	}{
		// Valid Twitter URLs
		{
			desc:         "basic twitter.com URL",
			input:        "https://twitter.com/elonmusk/status/1234567890",
			shouldMatch:  true,
			expectedUser: "elonmusk",
			expectedID:   "1234567890",
		},
		{
			desc:         "mobile twitter.com URL",
			input:        "https://mobile.twitter.com/jack/status/9876543210",
			shouldMatch:  true,
			expectedUser: "jack",
			expectedID:   "9876543210",
		},
		{
			desc:         "uppercase TWITTER.COM (case insensitive)",
			input:        "https://TWITTER.COM/user/status/123",
			shouldMatch:  true,
			expectedUser: "user",
			expectedID:   "123",
		},

		// Valid X URLs
		{
			desc:         "basic x.com URL",
			input:        "https://x.com/elonmusk/status/1234567890",
			shouldMatch:  true,
			expectedUser: "elonmusk",
			expectedID:   "1234567890",
		},
		{
			desc:         "mobile x.com URL",
			input:        "https://mobile.x.com/user_name/status/111222333",
			shouldMatch:  true,
			expectedUser: "user_name",
			expectedID:   "111222333",
		},
		{
			desc:         "uppercase X.COM (case insensitive)",
			input:        "https://X.COM/TestUser/status/999",
			shouldMatch:  true,
			expectedUser: "TestUser",
			expectedID:   "999",
		},
		{
			desc:         "x.com with query string",
			input:        "https://x.com/TestUser/status/999?a=b",
			shouldMatch:  true,
			expectedUser: "TestUser",
			expectedID:   "999",
		},

		// Valid Nitter URLs
		{
			desc:         "basic nitter.net URL",
			input:        "https://nitter.net/elonmusk/status/1234567890",
			shouldMatch:  true,
			expectedUser: "elonmusk",
			expectedID:   "1234567890",
		},
		{
			desc:         "mobile nitter.net URL",
			input:        "https://mobile.nitter.net/user/status/123",
			shouldMatch:  true,
			expectedUser: "user",
			expectedID:   "123",
		},

		// URL in context (with surrounding text)
		{
			desc:         "URL in message with surrounding text",
			input:        "Check this out https://twitter.com/user/status/123 it's cool",
			shouldMatch:  true,
			expectedUser: "user",
			expectedID:   "123",
		},
		{
			desc:         "URL at start of message",
			input:        "https://x.com/test/status/456 is interesting",
			shouldMatch:  true,
			expectedUser: "test",
			expectedID:   "456",
		},

		// Invalid URLs that shouldn't match
		{
			desc:        "http instead of https",
			input:       "http://twitter.com/user/status/123",
			shouldMatch: false,
		},
		{
			desc:        "missing status keyword",
			input:       "https://twitter.com/user/123",
			shouldMatch: false,
		},
		{
			desc:        "missing status ID",
			input:       "https://twitter.com/user/status/",
			shouldMatch: false,
		},
		{
			desc:        "non-numeric status ID",
			input:       "https://twitter.com/user/status/abc123",
			shouldMatch: false,
		},
		{
			desc:        "different domain",
			input:       "https://facebook.com/user/status/123",
			shouldMatch: false,
		},
		{
			desc:        "www subdomain (not currently supported)",
			input:       "https://www.twitter.com/user/status/123",
			shouldMatch: false,
		},
		{
			desc:         "missing username",
			input:        "https://twitter.com//status/123",
			shouldMatch:  true, // Actually matches with empty username due to .*?
			expectedUser: "",
			expectedID:   "123",
		},

		// Edge cases
		{
			desc:         "very long status ID",
			input:        "https://twitter.com/user/status/12345678901234567890",
			shouldMatch:  true,
			expectedUser: "user",
			expectedID:   "12345678901234567890",
		},
		{
			desc:         "username with underscore",
			input:        "https://twitter.com/user_name_123/status/789",
			shouldMatch:  true,
			expectedUser: "user_name_123",
			expectedID:   "789",
		},
		{
			desc:         "username with dots (not valid but regex allows)",
			input:        "https://twitter.com/user.name/status/456",
			shouldMatch:  true,
			expectedUser: "user.name",
			expectedID:   "456",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			matches := tweetRe.FindStringSubmatch(tc.input)

			if tc.shouldMatch {
				if matches == nil {
					t.Errorf("expected match but got none")
					return
				}
				if len(matches) != 3 {
					t.Errorf("expected 3 submatch groups, got %d", len(matches))
					return
				}

				actualUser := matches[1]
				actualID := matches[2]

				if actualUser != tc.expectedUser {
					t.Errorf("username mismatch: want %q, got %q", tc.expectedUser, actualUser)
				}
				if actualID != tc.expectedID {
					t.Errorf("status ID mismatch: want %q, got %q", tc.expectedID, actualID)
				}
			} else {
				if matches != nil {
					t.Errorf("expected no match but got: %v", matches)
				}
			}
		})
	}
}
