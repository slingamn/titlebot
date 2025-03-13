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
