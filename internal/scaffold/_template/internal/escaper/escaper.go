// Package escaper neutralizes characters that carry special meaning in a query language, so user input
// can be matched literally.
package escaper

import "strings"

var likeReplacer = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// LikeEscapeChar is the escape character Like uses. Pair it with `ESCAPE '\'` in the SQL.
const LikeEscapeChar = `\`

// Like escapes the SQL LIKE wildcards (% and _) and the escape character itself, so a search for
// "100%" or "_" matches those literal characters. The query must declare `ESCAPE '\'`.
func Like(term string) string {
	return likeReplacer.Replace(term)
}
