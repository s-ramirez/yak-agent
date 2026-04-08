package skills

// Skill represents a loaded skill definition parsed from a SKILL.md file.
type Skill struct {
	Name                   string
	Description            string
	Content                string
	DisableModelInvocation bool
	FilePath               string
}
