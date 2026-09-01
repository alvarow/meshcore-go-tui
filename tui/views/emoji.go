package views

import "regexp"

var shortcodeRe = regexp.MustCompile(`:(\w+):`)

// expandShortcodes replaces :shortcode: patterns with their emoji equivalent.
// Unknown codes are left unchanged.
func expandShortcodes(s string) string {
	return shortcodeRe.ReplaceAllStringFunc(s, func(match string) string {
		code := match[1 : len(match)-1]
		if e, ok := shortcodes[code]; ok {
			return e
		}
		return match
	})
}

var shortcodes = map[string]string{
	// Faces
	"smile":        "😊",
	"grin":         "😁",
	"laugh":        "😂",
	"joy":          "😂",
	"rofl":         "🤣",
	"wink":         "😉",
	"blush":        "😊",
	"neutral":      "😐",
	"thinking":     "🤔",
	"sad":          "😢",
	"cry":          "😭",
	"angry":        "😠",
	"rage":         "😡",
	"scream":       "😱",
	"sweat":        "😅",
	"cool":         "😎",
	"skull":        "💀",
	"zipper_mouth": "🤐",
	"shush":        "🤫",
	"nerd":         "🤓",

	// Gestures
	"thumbsup":   "👍",
	"+1":         "👍",
	"thumbsdown": "👎",
	"-1":         "👎",
	"wave":       "👋",
	"clap":       "👏",
	"pray":       "🙏",
	"muscle":     "💪",
	"ok":         "👌",
	"point_up":   "☝️",
	"raised":     "✋",
	"fist":       "✊",
	"v":          "✌️",
	"crossed":    "🤞",

	// Hearts & symbols
	"heart":         "❤️",
	"hearts":        "❤️",
	"broken_heart":  "💔",
	"fire":          "🔥",
	"star":          "⭐",
	"sparkles":      "✨",
	"100":           "💯",
	"tada":          "🎉",
	"party":         "🎊",
	"check":         "✅",
	"x":             "❌",
	"warning":       "⚠️",
	"sos":           "🆘",
	"question":      "❓",
	"exclamation":   "❗",
	"eyes":          "👀",
	"zap":           "⚡",
	"boom":          "💥",
	"rocket":        "🚀",

	// Tech / mesh context
	"signal":   "📶",
	"radio":    "📻",
	"antenna":  "📡",
	"lock":     "🔒",
	"unlock":   "🔓",
	"key":      "🔑",
	"gear":     "⚙️",
	"wrench":   "🔧",
	"phone":    "📱",
	"laptop":   "💻",
	"battery":  "🔋",
	"location": "📍",
	"map":      "🗺️",
	"compass":  "🧭",
}
