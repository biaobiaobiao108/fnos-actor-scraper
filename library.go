package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

var requiredPersonColumns = []string{"guid", "trim_id", "tmdb_id", "imdb_id", "name", "original_name"}

func loadLocalPeople(path string) ([]FnPerson, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(absolute)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	rows, err := db.Query("PRAGMA table_info(person)")
	if err != nil {
		return nil, err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, primary int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &primary); err != nil {
			rows.Close()
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	missing := []string{}
	for _, column := range requiredPersonColumns {
		if !columns[column] {
			missing = append(missing, column)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("FnOS person 表缺少字段：%s；数据库结构可能已随 FnOS 更新", strings.Join(missing, ", "))
	}
	query := `SELECT guid, COALESCE(trim_id,''), COALESCE(tmdb_id,0), COALESCE(imdb_id,''), COALESCE(name,''), COALESCE(original_name,''), COALESCE(biography,''), COALESCE(profile_path,'') FROM person WHERE substr(trim_id,1,13)='LOCAL_PERSON_' ORDER BY name COLLATE NOCASE`
	peopleRows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer peopleRows.Close()
	people := make([]FnPerson, 0, 256)
	for peopleRows.Next() {
		var person FnPerson
		if err := peopleRows.Scan(&person.GUID, &person.TrimID, &person.TMDBID, &person.IMDBID, &person.Name, &person.OriginalName, &person.Biography, &person.ProfilePath); err != nil {
			return nil, err
		}
		if strings.HasPrefix(strings.ToUpper(person.TrimID), "LOCAL_PERSON_") && person.TMDBID == 0 && person.IMDBID == "" {
			people = append(people, person)
		}
	}
	return people, peopleRows.Err()
}
