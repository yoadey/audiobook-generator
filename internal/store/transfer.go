package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"gorm.io/gorm"
)

// Tables of a book export, in the order they are restored (rows only refer
// to rows of earlier tables).
const (
	TableVolumes           = "volumes"
	TableCharacters        = "characters"
	TableChapters          = "chapters"
	TableParagraphs        = "paragraphs"
	TableSegments          = "segments"
	TableAliases           = "character_aliases"
	TableCharacterChapters = "character_chapters"
	TableDistincts         = "character_distincts"
	TableAnalysisBatches   = "analysis_batches"
	TableLexicon           = "lexicon_entries"
)

// ExportTables lists the exported tables in restore order.
var ExportTables = []string{
	TableVolumes, TableCharacters, TableChapters, TableParagraphs, TableSegments, TableAliases,
	TableCharacterChapters, TableDistincts, TableAnalysisBatches, TableLexicon,
}

const transferBatch = 500

// ExportBook streams a book's rows, table by table in ExportTables order,
// to emit (rows are the models of this package). Audio, jobs and listening
// positions are not part of an export.
func (s *Store) ExportBook(ctx context.Context, bookID int64, emit func(table string, row any) error) error {
	db := s.db.WithContext(ctx)
	chapters := s.db.Session(&gorm.Session{NewDB: true}).Model(&Chapter{}).Select("id").Where("book_id = ?", bookID)
	queries := map[string]func() *gorm.DB{
		TableVolumes:           func() *gorm.DB { return db.Model(&Volume{}).Where("book_id = ?", bookID) },
		TableCharacters:        func() *gorm.DB { return db.Model(&Character{}).Where("book_id = ?", bookID) },
		TableChapters:          func() *gorm.DB { return db.Model(&Chapter{}).Where("book_id = ?", bookID) },
		TableParagraphs:        func() *gorm.DB { return db.Model(&Paragraph{}).Where("chapter_id IN (?)", chapters) },
		TableSegments:          func() *gorm.DB { return db.Model(&Segment{}).Where("chapter_id IN (?)", chapters) },
		TableAliases:           func() *gorm.DB { return db.Model(&CharacterAlias{}).Where("book_id = ?", bookID) },
		TableCharacterChapters: func() *gorm.DB { return db.Model(&CharacterChapter{}).Where("book_id = ?", bookID) },
		TableDistincts:         func() *gorm.DB { return db.Model(&CharacterDistinct{}).Where("book_id = ?", bookID) },
		TableAnalysisBatches:   func() *gorm.DB { return db.Model(&AnalysisBatch{}).Where("book_id = ?", bookID) },
		TableLexicon:           func() *gorm.DB { return db.Model(&LexiconEntry{}).Where("book_id = ?", bookID) },
	}
	for _, table := range ExportTables {
		if err := exportTable(queries[table](), table, emit); err != nil {
			return fmt.Errorf("export %s: %w", table, err)
		}
	}
	return nil
}

// exportTable pages through a table by ID (keyset pagination, so huge
// tables never sit in memory at once).
func exportTable(q *gorm.DB, table string, emit func(string, any) error) error {
	var last int64
	for {
		var n int
		var err error
		page := q.Session(&gorm.Session{}).Where("id > ?", last).Order("id").Limit(transferBatch)
		switch table {
		case TableVolumes:
			n, last, err = emitPage[Volume](page, table, emit, func(r *Volume) int64 { return r.ID })
		case TableCharacters:
			n, last, err = emitPage[Character](page, table, emit, func(r *Character) int64 { return r.ID })
		case TableChapters:
			n, last, err = emitPage[Chapter](page, table, emit, func(r *Chapter) int64 { return r.ID })
		case TableParagraphs:
			n, last, err = emitPage[Paragraph](page, table, emit, func(r *Paragraph) int64 { return r.ID })
		case TableSegments:
			n, last, err = emitPage[Segment](page, table, emit, func(r *Segment) int64 { return r.ID })
		case TableAliases:
			n, last, err = emitPage[CharacterAlias](page, table, emit, func(r *CharacterAlias) int64 { return r.ID })
		case TableCharacterChapters:
			n, last, err = emitPage[CharacterChapter](page, table, emit, func(r *CharacterChapter) int64 { return r.ID })
		case TableDistincts:
			n, last, err = emitPage[CharacterDistinct](page, table, emit, func(r *CharacterDistinct) int64 { return r.ID })
		case TableAnalysisBatches:
			n, last, err = emitPage[AnalysisBatch](page, table, emit, func(r *AnalysisBatch) int64 { return r.ID })
		case TableLexicon:
			n, last, err = emitPage[LexiconEntry](page, table, emit, func(r *LexiconEntry) int64 { return r.ID })
		default:
			return fmt.Errorf("unknown table %q", table)
		}
		if err != nil {
			return err
		}
		if n < transferBatch {
			return nil
		}
	}
}

func emitPage[T any](q *gorm.DB, table string, emit func(string, any) error, id func(*T) int64) (int, int64, error) {
	var rows []T
	if err := q.Find(&rows).Error; err != nil {
		return 0, 0, err
	}
	var last int64
	for i := range rows {
		if err := emit(table, &rows[i]); err != nil {
			return 0, 0, err
		}
		last = id(&rows[i])
	}
	return len(rows), last, nil
}

// RowReader yields the exported rows of one table: Next decodes the next
// row into dst and reports false at the end.
type RowReader interface {
	Next(dst any) (bool, error)
}

// RestoreBook replaces the content of book bookID with exported rows. IDs
// are assigned anew and references remapped. open returns the rows of a
// table (nil: none). Restored work that was in progress is left
// resumable: a running casting or attribution starts over. Audio is not
// restored.
//
// The rows are written batch by batch, not in one transaction: on SQLite a
// transaction over a huge book would block every other writer for minutes.
// The book is not usable while it is restored, and a restore starts by
// deleting what an earlier attempt left, so a failed one can run again.
func (s *Store) RestoreBook(ctx context.Context, bookID int64, open func(table string) (RowReader, error)) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := deleteBookContent(tx, bookID); err != nil {
			return err
		}
		return tx.Where("book_id = ?", bookID).Delete(&LexiconEntry{}).Error
	})
	if err != nil {
		return err
	}
	r := restorer{tx: s.db.WithContext(ctx), bookID: bookID, ids: map[string]map[int64]int64{}}
	for _, table := range ExportTables {
		rows, err := open(table)
		if err != nil {
			return err
		}
		if rows == nil {
			continue
		}
		if err := r.table(table, rows); err != nil {
			return fmt.Errorf("restore %s: %w", table, err)
		}
	}
	return nil
}

type restorer struct {
	tx     *gorm.DB
	bookID int64
	ids    map[string]map[int64]int64 // table → old ID → new ID
}

// errSkip drops a row whose parent is not in the export. An export is
// read without a transaction (on SQLite one would block every writer for
// its duration), so a row deleted meanwhile (a character merged away) can
// still be referred to.
var errSkip = errors.New("skip row")

// ref maps an exported ID of table to the restored one.
func (r *restorer) ref(table string, old int64) (int64, error) {
	if id, ok := r.ids[table][old]; ok && id != 0 {
		return id, nil
	}
	return 0, errSkip
}

// optRef maps an optional reference; one to a missing row becomes nil.
func (r *restorer) optRef(table string, old *int64) (*int64, error) {
	if old == nil {
		return nil, nil
	}
	id, err := r.ref(table, *old)
	if err != nil {
		return nil, nil //nolint:nilerr // a dangling optional reference is dropped
	}
	return &id, nil
}

func (r *restorer) table(table string, rows RowReader) error {
	switch table {
	case TableVolumes:
		return restoreRows(r, table, rows, func(v *Volume) error {
			v.BookID = r.bookID
			return nil
		})
	case TableCharacters:
		return restoreRows(r, table, rows, func(c *Character) error {
			c.BookID = r.bookID
			// The API's bound: a longer sample text fails every preview.
			if t := []rune(CleanText(c.PreviewText)); len(t) > 500 {
				c.PreviewText = string(t[:500])
			} else {
				c.PreviewText = string(t)
			}
			return nil
		})
	case TableChapters:
		return restoreRows(r, table, rows, func(c *Chapter) error {
			var err error
			c.BookID = r.bookID
			if c.VolumeID, err = r.optRef(TableVolumes, c.VolumeID); err != nil {
				return err
			}
			if c.POVCharacterID, err = r.optRef(TableCharacters, c.POVCharacterID); err != nil {
				return err
			}
			// No audio comes along; outdated audio cannot exist.
			c.AudioStatus, c.AudioPath, c.AudioDurationMs, c.AudioSize = AudioNone, "", 0, 0
			c.AudioError, c.AudioVoice, c.AudioUpdatedAt, c.AudioVoiceRev = "", "", nil, 0
			if c.AttributionStatus == AttributionQueued || c.AttributionStatus == AttributionRunning {
				c.AttributionStatus, c.AttributionPart = AttributionNone, 0
			}
			if c.PrepStatus == PrepQueued || c.PrepStatus == PrepRunning || c.PrepStatus == "" {
				c.PrepStatus, c.PrepPart = PrepNone, 0
			}
			return nil
		})
	case TableParagraphs:
		return restoreRows(r, table, rows, func(p *Paragraph) error {
			var err error
			p.ChapterID, err = r.ref(TableChapters, p.ChapterID)
			return err
		})
	case TableSegments:
		return restoreRows(r, table, rows, func(sg *Segment) error {
			var err error
			if sg.ChapterID, err = r.ref(TableChapters, sg.ChapterID); err != nil {
				return err
			}
			if sg.ParagraphID, err = r.ref(TableParagraphs, sg.ParagraphID); err != nil {
				return err
			}
			sg.SpeakerCharacterID, err = r.optRef(TableCharacters, sg.SpeakerCharacterID)
			return err
		})
	case TableAliases:
		return restoreRows(r, table, rows, func(a *CharacterAlias) error {
			var err error
			a.BookID = r.bookID
			a.CharacterID, err = r.ref(TableCharacters, a.CharacterID)
			return err
		})
	case TableCharacterChapters:
		return restoreRows(r, table, rows, func(cc *CharacterChapter) error {
			var err error
			cc.BookID = r.bookID
			if cc.CharacterID, err = r.ref(TableCharacters, cc.CharacterID); err != nil {
				return err
			}
			cc.ChapterID, err = r.ref(TableChapters, cc.ChapterID)
			return err
		})
	case TableDistincts:
		return restoreRows(r, table, rows, func(d *CharacterDistinct) error {
			var err error
			d.BookID = r.bookID
			if d.AID, err = r.ref(TableCharacters, d.AID); err != nil {
				return err
			}
			if d.BID, err = r.ref(TableCharacters, d.BID); err != nil {
				return err
			}
			if d.AID > d.BID {
				d.AID, d.BID = d.BID, d.AID
			}
			return nil
		})
	case TableAnalysisBatches:
		return restoreRows(r, table, rows, func(b *AnalysisBatch) error {
			b.BookID = r.bookID
			var parts []map[string]any
			if err := json.Unmarshal([]byte(b.Parts), &parts); err != nil {
				return fmt.Errorf("batch parts: %w", err)
			}
			for _, p := range parts {
				old, ok := p["chapterId"].(float64)
				if !ok {
					return errors.New("batch part without chapterId")
				}
				id, err := r.ref(TableChapters, int64(old))
				if err != nil {
					return err
				}
				p["chapterId"] = id
			}
			data, err := json.Marshal(parts)
			b.Parts = string(data)
			return err
		})
	case TableLexicon:
		return restoreRows(r, table, rows, func(e *LexiconEntry) error {
			e.BookID = r.bookID
			return nil
		})
	}
	return fmt.Errorf("unknown table %q", table)
}

// restoreRows inserts a table's rows in batches with new IDs. prepare
// remaps a row's references; the old IDs of the rows are recorded against
// the new ones for the tables that come after.
func restoreRows[T any](r *restorer, table string, rows RowReader, prepare func(*T) error) error {
	ids := map[int64]int64{}
	r.ids[table] = ids
	batch := make([]T, 0, transferBatch)
	olds := make([]int64, 0, transferBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := r.tx.Create(&batch).Error; err != nil {
			return err
		}
		for i := range batch {
			ids[olds[i]] = idField(&batch[i]).Int()
		}
		batch, olds = batch[:0], olds[:0]
		return nil
	}
	for {
		var row T
		ok, err := rows.Next(&row)
		if err != nil {
			return err
		}
		if !ok {
			return flush()
		}
		if err := prepare(&row); errors.Is(err, errSkip) {
			continue
		} else if err != nil {
			return err
		}
		id := idField(&row)
		if _, dup := ids[id.Int()]; dup || id.Int() <= 0 {
			return fmt.Errorf("invalid or duplicate ID %d", id.Int())
		}
		ids[id.Int()] = 0 // reserved until the batch is written
		olds = append(olds, id.Int())
		id.SetInt(0) // the database assigns a new one
		batch = append(batch, row)
		if len(batch) == transferBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

// idField is the ID field of a model.
func idField(row any) reflect.Value {
	return reflect.ValueOf(row).Elem().FieldByName("ID")
}
