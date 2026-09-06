//go:build ignore

package main

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	db, err := sql.Open("mysql", "root:gkc2020..@tcp(192.168.1.2:3306)/test?charset=utf8mb4")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, status, left(name,30), created_at FROM tasks ORDER BY created_at ASC`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, status, name string
		var created int64
		_ = rows.Scan(&id, &status, &name, &created)
		fmt.Println(id, status, name, created)
		n++
	}
	fmt.Println("total rows:", n)
}
