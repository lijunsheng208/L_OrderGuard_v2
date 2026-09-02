package knowledge

import (
	"embed"
	"path"
	"sort"
	"strings"
)

//go:embed files/*.md
var files embed.FS

// Section 是按 Markdown 标题切分后的知识片段。
type Section struct {
	Source  string `json:"source"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// Store 提供只读的本地 Markdown 知识库。
type Store struct{ sections []Section }

// NewStore 加载内嵌 Markdown 并按标题切分。
func NewStore() (*Store, error) {
	entries, err := files.ReadDir("files")
	if err != nil {
		return nil, err
	}
	var sections []Section
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := files.ReadFile(path.Join("files", entry.Name()))
		if err != nil {
			return nil, err
		}
		sections = append(sections, split(entry.Name(), string(data))...)
	}
	sort.Slice(sections, func(i, j int) bool {
		return sections[i].Source+sections[i].Title < sections[j].Source+sections[j].Title
	})
	return &Store{sections: sections}, nil
}

// Search 按标题和正文中的词项返回最相关片段。
func (s *Store) Search(query string, limit int) []Section {
	terms := strings.Fields(strings.ToLower(query))
	if limit <= 0 {
		limit = 10
	}
	type hit struct {
		section Section
		score   int
	}
	hits := make([]hit, 0)
	for _, section := range s.sections {
		text := strings.ToLower(section.Title + " " + section.Content)
		score := 0
		for _, term := range terms {
			if strings.Contains(text, term) {
				score++
			}
			if strings.Contains(strings.ToLower(section.Title), term) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, hit{section, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	result := make([]Section, len(hits))
	for i := range hits {
		result[i] = hits[i].section
	}
	return result
}

// split 按 ATX 标题切分文档，保留标题以下的正文。
func split(source, document string) []Section {
	lines := strings.Split(strings.ReplaceAll(document, "\r\n", "\n"), "\n")
	var result []Section
	title := ""
	body := make([]string, 0)
	flush := func() {
		content := strings.TrimSpace(strings.Join(body, "\n"))
		if title != "" {
			result = append(result, Section{Source: "knowledge/" + source, Title: title, Content: content})
		}
		body = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			hashes := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if hashes > 0 && hashes <= 6 && len(trimmed) > hashes && trimmed[hashes] == ' ' {
				flush()
				title = strings.TrimSpace(trimmed[hashes:])
				continue
			}
		}
		body = append(body, line)
	}
	flush()
	return result
}
