package assets

// migrations are the catalog schema, one step per version (spec/assets.md, "Database"). They are forward-only:
// never edit or reorder a step that shipped; append a new one. PRAGMA user_version holds the number run.
var migrations = []string{
	// 1: entries, spans, clocks, transcripts, derived files, the hash cache, kits and the search index.
	`
CREATE TABLE assets (
	id              TEXT PRIMARY KEY,                   -- proj:…, lib:…, brand:<kit>@<version>/…
	scope           TEXT NOT NULL CHECK (scope IN ('project', 'brand', 'lib')),
	kind            TEXT NOT NULL CHECK (kind IN ('video', 'audio', 'image', 'font', 'other')),
	hash            TEXT,                               -- b3:<hex>; NULL until hashed (metadata-only imports)
	size            INTEGER,                            -- bytes
	mtime           INTEGER,                            -- the in-place file's mtime (ns) when it was hashed
	locator         TEXT NOT NULL,                      -- file:///… (in place) or blob:b3:<hex><.ext>
	in_store        INTEGER NOT NULL DEFAULT 0,
	media           TEXT NOT NULL DEFAULT '{}',         -- JSON, from the probe
	proxy           TEXT,                               -- JSON {hash, size, gop} or NULL
	title           TEXT NOT NULL DEFAULT '',
	tags            TEXT NOT NULL DEFAULT '[]',         -- JSON list
	caption         TEXT NOT NULL DEFAULT '',
	previews        TEXT NOT NULL DEFAULT '{}',         -- JSON {contact_sheet, every_s, waveform, ...}: blob refs
	license         TEXT NOT NULL DEFAULT 'review-only'
	                CHECK (license IN ('owned', 'cleared', 'attribution', 'review-only', 'blocked')),
	credit          TEXT NOT NULL DEFAULT '',
	license_source  TEXT NOT NULL DEFAULT '',
	license_note    TEXT NOT NULL DEFAULT '',
	content_id_risk TEXT NOT NULL DEFAULT '' CHECK (content_id_risk IN ('', 'low', 'medium', 'high')),
	phash           TEXT NOT NULL DEFAULT '',           -- images: 16 hex digits of a 64-bit dHash
	near            TEXT NOT NULL DEFAULT '[]',         -- JSON [{id, distance}] found at import
	provenance      TEXT NOT NULL DEFAULT '{}',         -- JSON
	plugin          TEXT NOT NULL DEFAULT '',           -- the plug-in that added it, if any
	plugin_key      TEXT,                               -- its stable key there
	meta            TEXT NOT NULL DEFAULT '{}',         -- JSON the core never reads
	created         TEXT NOT NULL,
	updated         TEXT NOT NULL
);
CREATE UNIQUE INDEX assets_plugin_key ON assets (plugin, plugin_key) WHERE plugin_key IS NOT NULL;
CREATE INDEX assets_hash ON assets (hash);
CREATE INDEX assets_kind ON assets (kind, updated);

CREATE TABLE spans (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	asset           TEXT NOT NULL REFERENCES assets (id) ON DELETE CASCADE ON UPDATE CASCADE,
	t0              REAL NOT NULL,
	t1              REAL NOT NULL,
	type            TEXT NOT NULL,
	label           TEXT NOT NULL DEFAULT '',
	data            TEXT NOT NULL DEFAULT '{}',         -- JSON object
	source_of_label TEXT NOT NULL DEFAULT 'manual',
	confidence      TEXT NOT NULL DEFAULT '',
	plugin          TEXT NOT NULL DEFAULT '',
	key             TEXT,                               -- a plug-in's stable key
	created         TEXT NOT NULL,
	updated         TEXT NOT NULL,
	CHECK (t1 >= t0)
);
CREATE UNIQUE INDEX spans_key ON spans (plugin, key) WHERE key IS NOT NULL;
CREATE INDEX spans_asset ON spans (asset, t0);
CREATE INDEX spans_type ON spans (type);

CREATE TABLE clocks (
	asset      TEXT NOT NULL REFERENCES assets (id) ON DELETE CASCADE ON UPDATE CASCADE,
	domain     TEXT NOT NULL,                           -- <plugin>:<thing>, e.g. slate:take/7
	plugin     TEXT NOT NULL DEFAULT '',
	epochs     TEXT NOT NULL DEFAULT '[]',              -- JSON [{asset_t: [a, b], domain_t0, rate}]
	confidence TEXT NOT NULL DEFAULT '',
	method     TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (asset, domain)
);
CREATE INDEX clocks_domain ON clocks (domain);

CREATE TABLE span_types (
	type        TEXT PRIMARY KEY,
	plugin      TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	data_schema TEXT NOT NULL DEFAULT '{}'
);
INSERT INTO span_types (type, description) VALUES
	('tag', 'A label on a moment, by a person or an agent'),
	('shot', 'A shot: from one cut to the next'),
	('speech', 'Someone speaking');

CREATE TABLE transcripts (
	asset   TEXT PRIMARY KEY REFERENCES assets (id) ON DELETE CASCADE ON UPDATE CASCADE,
	model   TEXT NOT NULL,
	track   INTEGER NOT NULL DEFAULT 0,
	words   TEXT NOT NULL,                              -- JSON [[start, end, word], ...]
	text    TEXT NOT NULL,
	created TEXT NOT NULL
);

CREATE TABLE derived (
	asset   TEXT NOT NULL REFERENCES assets (id) ON DELETE CASCADE ON UPDATE CASCADE,
	recipe  TEXT NOT NULL,                              -- e.g. "contact_sheet every=60", "frame t=12.500 w=960"
	source  TEXT NOT NULL,                              -- the input's identity: its hash, or size:mtime
	blob    TEXT NOT NULL,                              -- b3:<hex><.ext>
	meta    TEXT NOT NULL DEFAULT '{}',
	created TEXT NOT NULL,
	PRIMARY KEY (asset, recipe)
);

CREATE TABLE hash_cache (
	path  TEXT PRIMARY KEY,
	size  INTEGER NOT NULL,
	mtime INTEGER NOT NULL,
	hash  TEXT NOT NULL
);

CREATE TABLE kits (
	name      TEXT NOT NULL,
	version   TEXT NOT NULL,
	title     TEXT NOT NULL DEFAULT '',
	tokens    TEXT NOT NULL DEFAULT '{}',
	manifest  TEXT NOT NULL,                            -- kit.json as installed
	installed TEXT NOT NULL,
	PRIMARY KEY (name, version)
);

CREATE VIRTUAL TABLE assets_fts USING fts5 (id UNINDEXED, title, tags, caption, transcript,
	tokenize = 'unicode61 remove_diacritics 2');
CREATE VIRTUAL TABLE spans_fts USING fts5 (span UNINDEXED, label, type, data,
	tokenize = 'unicode61 remove_diacritics 2');
`,
}

// SchemaVersion is the catalog schema version this build writes.
var SchemaVersion = len(migrations)
