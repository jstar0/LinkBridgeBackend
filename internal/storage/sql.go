package storage

import (
	"strconv"
	"strings"
)

func (s *Store) rebind(query string) string {
	return rebindQuery(s.driver, query)
}

func rebindQuery(driver, query string) string {
	if driver != "pgx" {
		return query
	}
	return rebindToPostgres(query)
}

func rebindToPostgres(query string) string {
	// Convert '?' placeholders into Postgres-style '$1, $2, ...'.
	// This is intentionally minimal and only supports the SQL we write in this codebase.
	var b strings.Builder
	b.Grow(len(query) + 8)

	inSingleQuotes := false
	argIndex := 1

	for i := 0; i < len(query); i++ {
		ch := query[i]

		if ch == '\'' {
			// Handle escaped quotes inside string literals: '' (two single quotes).
			if inSingleQuotes && i+1 < len(query) && query[i+1] == '\'' {
				b.WriteByte('\'')
				b.WriteByte('\'')
				i++
				continue
			}
			inSingleQuotes = !inSingleQuotes
			b.WriteByte(ch)
			continue
		}

		if ch == '?' && !inSingleQuotes {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(argIndex))
			argIndex++
			continue
		}

		b.WriteByte(ch)
	}

	return b.String()
}
