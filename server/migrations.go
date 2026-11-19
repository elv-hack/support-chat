package main

import (
	"database/sql"
	"fmt"
)

const schemaVersion = 4

// All migrations are atomic; an old executable refuses a newer database schema.
func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema %d is newer than supported schema %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return tx.Commit()
	}
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,username TEXT UNIQUE NOT NULL,name TEXT NOT NULL,role TEXT NOT NULL,hash BLOB NOT NULL,disabled INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS sends(id TEXT PRIMARY KEY,customer TEXT NOT NULL,sender TEXT NOT NULL,fingerprint TEXT NOT NULL,remote_id TEXT NOT NULL DEFAULT '',state TEXT NOT NULL,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS attachments(id TEXT PRIMARY KEY,customer TEXT NOT NULL,owner TEXT NOT NULL,name TEXT NOT NULL,mime TEXT NOT NULL,kind TEXT NOT NULL,size INTEGER NOT NULL,duration REAL NOT NULL DEFAULT 0,send_id TEXT NOT NULL DEFAULT '',deleted INTEGER NOT NULL DEFAULT 0,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS deletions(customer TEXT NOT NULL,seq INTEGER NOT NULL,PRIMARY KEY(customer,seq));
 CREATE TABLE IF NOT EXISTS deletion_done(customer TEXT NOT NULL,seq INTEGER NOT NULL,PRIMARY KEY(customer,seq));
 CREATE TABLE IF NOT EXISTS purge_ranges(customer TEXT PRIMARY KEY,upto INTEGER NOT NULL,cursor INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS sync_cursors(customer TEXT PRIMARY KEY,cursor INTEGER NOT NULL DEFAULT 1);
 CREATE TABLE IF NOT EXISTS conversations(customer TEXT PRIMARY KEY,cleared INTEGER NOT NULL DEFAULT 0,agent_read INTEGER NOT NULL DEFAULT 0,customer_read INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS attachment_customer ON attachments(customer);
 CREATE INDEX IF NOT EXISTS send_customer ON sends(customer,created);`); err != nil {
		return err
	}
	rows, err := tx.Query("PRAGMA table_info(sends)")
	if err != nil {
		return err
	}
	hasSeq := false
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "seq" {
			hasSeq = true
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !hasSeq {
		if _, err = tx.Exec("ALTER TABLE sends ADD COLUMN seq INTEGER NOT NULL DEFAULT 0; DELETE FROM sync_cursors;"); err != nil {
			return err
		}
	}

	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS library_entries(id TEXT PRIMARY KEY,kind TEXT NOT NULL,title TEXT NOT NULL,category TEXT NOT NULL,sections TEXT NOT NULL,version INTEGER NOT NULL,updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS library_images(id TEXT PRIMARY KEY,name TEXT NOT NULL,mime TEXT NOT NULL,size INTEGER NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS library_deliveries(id TEXT PRIMARY KEY,customer TEXT NOT NULL,owner TEXT NOT NULL,title TEXT NOT NULL,fingerprint TEXT NOT NULL,messages TEXT NOT NULL,created INTEGER NOT NULL,cancelled INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS library_delivery_customer ON library_deliveries(customer,created);
CREATE TABLE IF NOT EXISTS library_delivery_assets(delivery TEXT NOT NULL,attachment TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS app_settings(id INTEGER PRIMARY KEY CHECK(id=1),payload TEXT NOT NULL,revision INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS service_identity(id INTEGER PRIMARY KEY CHECK(id=1),value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS message_edits(customer TEXT NOT NULL,seq INTEGER NOT NULL,message_id TEXT NOT NULL,payload TEXT NOT NULL,revision INTEGER NOT NULL,edited_at INTEGER NOT NULL,PRIMARY KEY(customer,seq));
PRAGMA user_version=4;`); err != nil {
		return err
	}
	if version < 3 {
		rows, e := tx.Query("PRAGMA table_info(users)")
		if e != nil {
			return e
		}
		found := false
		for rows.Next() {
			var cid, nn, pk int
			var name, typ string
			var def any
			if e = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); e != nil {
				rows.Close()
				return e
			}
			if name == "permissions" {
				found = true
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if !found {
			if _, err = tx.Exec("ALTER TABLE users ADD COLUMN permissions INTEGER NOT NULL DEFAULT 15"); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec("INSERT OR IGNORE INTO service_identity(id,value) VALUES(1,?)", randomID()); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT OR IGNORE INTO app_settings(id,payload,revision) VALUES(1,?,1)", defaultSettingsJSON); err != nil {
		return err
	}
	if version < 4 {
		if _, err = tx.Exec(`UPDATE app_settings SET payload=replace(payload,'"primaryColor":"#275b4c"','"primaryColor":"#71f6b1"'),revision=revision+1 WHERE instr(payload,'"primaryColor":"#275b4c"')>0`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
