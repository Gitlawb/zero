package repoinfo

// extToLanguage maps a lowercase file extension (no dot) to a display language.
var extToLanguage = map[string]string{
	"go": "Go", "ts": "TypeScript", "tsx": "TypeScript", "js": "JavaScript",
	"jsx": "JavaScript", "mjs": "JavaScript", "cjs": "JavaScript",
	"py": "Python", "rs": "Rust", "java": "Java", "kt": "Kotlin", "kts": "Kotlin",
	"c": "C", "h": "C", "cc": "C++", "cpp": "C++", "cxx": "C++", "hpp": "C++", "hh": "C++",
	"cs": "C#", "rb": "Ruby", "php": "PHP", "swift": "Swift", "scala": "Scala",
	"sh": "Shell", "bash": "Shell", "zsh": "Shell",
	"sql": "SQL", "html": "HTML", "htm": "HTML", "css": "CSS", "scss": "SCSS", "sass": "SCSS",
	// Pure data/prose formats (json/yaml/toml/md) are intentionally NOT languages:
	// they would drown out the "primary language" signal. Markup-code (html/css)
	// is kept since it is authored frontend code.
	"proto": "Protobuf", "lua": "Lua", "dart": "Dart", "ex": "Elixir", "exs": "Elixir",
	"clj": "Clojure", "hs": "Haskell", "ml": "OCaml", "r": "R", "jl": "Julia",
	"vue": "Vue", "svelte": "Svelte", "pl": "Perl", "pm": "Perl", "groovy": "Groovy",
	"tf": "Terraform", "zig": "Zig", "nim": "Nim",
}

// languageForExt returns the language for a lowercase extension (no leading dot).
func languageForExt(ext string) (string, bool) {
	lang, ok := extToLanguage[ext]
	return lang, ok
}
