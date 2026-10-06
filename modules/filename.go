package modules

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type parsedMedia struct {
	Title, Year, Directory string
	Season, Episode        int
	TV                     bool
}

var episodePatterns = []*regexp.Regexp{regexp.MustCompile(`(?i)s(\d+)\s*[-. ]?\s*e\s*(\d+)`), regexp.MustCompile(`(?i)s(\d+)\s*-\s*(\d+)`), regexp.MustCompile(`(?i)[ ._\-](\d+)x(\d+)`), regexp.MustCompile(`(?i)season\s*(\d+)\s*[,\-]?\s*(?:episode|ep?)\.?\s*(\d+)`), regexp.MustCompile(`(?i)s(\d+)\s*(?:episode|ep)\.?\s*(\d+)`)}
var animeEpisode = regexp.MustCompile(`\s-\s*(\d+)(?:\s*-\s*|\s*\(|[vV]\d+\s*$|\s*$)`)
var yearToken = regexp.MustCompile(`(?:19|20)\d{2}`)
var releaseTag = regexp.MustCompile(`(?i)^(?:web[ .\-]?dl|webrip|blu[ .\-]?ray|b[dr]rip|hdrip|dvdrip|hdtv|amzn|nf|dsnp|hmax|proper|repack|remux|[xh][ .\-]?26[45]|hevc|avc|av1|aac|e[ .\-]?ac3|ac3|dts[ .\-]?hd|dts|truehd|ddp\d*|atmos|flac|opus|mp3|[257][ .]1(?:[ .]\d)?|(?:480|576|720|1080|2160|4320)[pi]|[248]k|(?:8|10|12)[ .\-]?bit|hdr10\+?|hdr|sdr|judas|subsplease|horriblesubs)$`)
var releaseGroups = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*judas\s*-\s*`),
	regexp.MustCompile(`(?i)^\s*subsplease\s*-\s*`),
	regexp.MustCompile(`(?i)^\s*horriblesubs\s*-\s*`),
}
var titleSeparators = strings.NewReplacer(".", " ", "_", " ")
var brackets = regexp.MustCompile(`\[[^\]]*\]`)
var releaseTail = regexp.MustCompile(`[\s._\-]+([^\s._]+(?:[ .\-][^\s._]+)?)$`)

func trimTitle(s string) string { return strings.Join(strings.Fields(s), " ") }
func knownRelease(s string) bool {
	if releaseTag.MatchString(s) {
		return true
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune(" ,;/_-", r) })
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if !releaseTag.MatchString(p) {
			return false
		}
	}
	return true
}
func stripRelease(s string) string {
	s = brackets.ReplaceAllStringFunc(s, func(b string) string {
		if knownRelease(b[1 : len(b)-1]) {
			return " "
		}
		return b
	})
	for _, re := range releaseGroups {
		s = re.ReplaceAllString(s, "")
	}
	for {
		old := s
		s = strings.TrimRight(s, " ._-\t")
		loc := releaseTail.FindStringSubmatchIndex(s)
		if loc != nil && releaseTag.MatchString(s[loc[2]:loc[3]]) {
			s = s[:loc[0]]
		} else {
			parts := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '.' || r == '_' || r == '-' })
			if len(parts) > 1 && releaseTag.MatchString(parts[len(parts)-1]) {
				last := strings.LastIndex(strings.ToLower(s), strings.ToLower(parts[len(parts)-1]))
				s = s[:last]
			}
		}
		if s == old {
			break
		}
	}
	return s
}
func extractYear(s string) (string, int) {
	year, start := "", -1
	for _, loc := range yearToken.FindAllStringIndex(s, -1) {
		a, b := loc[0], loc[1]
		if a > 0 && s[a-1] == '(' && b < len(s) && s[b] == ')' {
			year, start = s[a:b], a-1
		}
	}
	if start >= 0 {
		return year, start
	}
	for _, loc := range yearToken.FindAllStringIndex(s, -1) {
		a, b := loc[0], loc[1]
		if a > 0 && s[a-1] >= '0' && s[a-1] <= '9' || b < len(s) && s[b] >= '0' && s[b] <= '9' {
			continue
		}
		if a > 0 && s[a-1] == '(' && b < len(s) && s[b] == ')' {
			year, start = s[a:b], a-1
			continue
		}
		if trimTitle(strings.Trim(s[:a], " ._-")) != "" {
			year, start = s[a:b], a
		}
	}
	return year, start
}
func normalizeTitle(s string) string {
	return trimTitle(strings.Trim(titleSeparators.Replace(stripRelease(s)), " -"))
}
func parseFilename(path string) parsedMedia {
	path = strings.ReplaceAll(path, `\`, "/")
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	name = stripRelease(name)
	p := parsedMedia{}
	cut := len(name)
	for _, re := range episodePatterns {
		loc := re.FindStringSubmatchIndex(name)
		if loc != nil {
			p.Season, _ = strconv.Atoi(name[loc[2]:loc[3]])
			p.Episode, _ = strconv.Atoi(name[loc[4]:loc[5]])
			p.TV = true
			cut = loc[0]
			break
		}
	}
	if !p.TV {
		loc := animeEpisode.FindStringSubmatchIndex(name)
		if loc != nil {
			p.Season = 1
			p.Episode, _ = strconv.Atoi(name[loc[2]:loc[3]])
			p.TV = true
			cut = loc[0]
		}
	}
	year, at := extractYear(name)
	p.Year = year
	if at >= 0 && at < cut {
		cut = at
	}
	p.Title = normalizeTitle(name[:cut])
	dir := filepath.Base(filepath.Dir(path))
	dy, da := extractYear(dir)
	if da >= 0 {
		dir = dir[:da]
	}
	if dy != "" {
		p.Directory = normalizeTitle(dir)
	}
	if p.Year == "" {
		p.Year = dy
	}
	if p.Title == "" {
		p.Title = p.Directory
	}
	return p
}

// Normalize ASCII punctuation and letter case for title-index lookup.
func normalizeIndex(s string) string {
	return trimTitle(strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		if r < 128 && (unicode.IsSpace(r) || r >= 32 && r <= 47 || r >= 58 && r <= 64 || r >= 91 && r <= 96 || r >= 123 && r <= 126) {
			return ' '
		}
		return r
	}, s))
}
func normalizeMatch(s string) string { return normalizeIndex(strings.ReplaceAll(s, "&", " and ")) }
func similarity(a, b string) float64 {
	a, b = normalizeMatch(a), normalizeMatch(b)
	if a == b && a != "" {
		return 1
	}
	aa, bb := strings.Fields(a), strings.Fields(b)
	if len(aa) == 0 || len(bb) == 0 {
		return 0
	}
	counts := map[string]int{}
	for _, w := range aa {
		counts[w]++
	}
	common := 0
	for _, w := range bb {
		if counts[w] > 0 {
			common++
			counts[w]--
		}
	}
	return max(float64(2*common)/float64(len(aa)+len(bb)), .8*float64(common)/float64(min(len(aa), len(bb))))
}
func queries(p parsedMedia) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, q := range []string{p.Title, strings.TrimSpace(regexp.MustCompile(`^(?:\[[^\]]*\]\s*)+`).ReplaceAllString(p.Title, "")), p.Directory, strings.TrimSpace(regexp.MustCompile(`^(?:\[[^\]]*\]\s*)+`).ReplaceAllString(p.Directory, ""))} {
		key := normalizeMatch(q)
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, q)
		}
	}
	return out
}
