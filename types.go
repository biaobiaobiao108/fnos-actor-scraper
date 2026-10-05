package main

type ActorProfile struct {
	Name            string              `json:"name"`
	Aliases         []string            `json:"aliases"`
	ImageURL        string              `json:"imageUrl,omitempty"`
	ImageCandidates []PortraitCandidate `json:"imageCandidates,omitempty"`
	Biography       string              `json:"biography,omitempty"`
	BiographySource string              `json:"biographySource,omitempty"`
	Birthday        string              `json:"birthday,omitempty"`
	SourceURLs      []string            `json:"sourceUrls"`
	SourceNames     []string            `json:"sourceNames"`
}

type PortraitCandidate struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

type FnPerson struct {
	GUID              string `json:"guid"`
	TrimID            string `json:"trim_id,omitempty"`
	TMDBID            int64  `json:"tmdb_id,omitempty"`
	IMDBID            string `json:"imdb_id,omitempty"`
	Name              string `json:"name,omitempty"`
	OriginalName      string `json:"original_name,omitempty"`
	Biography         string `json:"biography,omitempty"`
	ProfilePath       string `json:"profile_path,omitempty"`
	IsOfficial        bool   `json:"is_official,omitempty"`
	NameLocked        bool   `json:"name_locked,omitempty"`
	BiographyLocked   bool   `json:"biography_locked,omitempty"`
	ProfilePathLocked bool   `json:"profile_path_locked,omitempty"`
}
