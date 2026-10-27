package attachment_test

import (
	"ai-pm-system/internal/attachment"
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestReadableAttachmentsAndValidation(t *testing.T) {
	var pngFile bytes.Buffer
	if err := png.Encode(&pngFile, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	a, err := attachment.Prepare("project", "clipboard.png", pngFile.Bytes())
	if err != nil || a.Kind != "image" || a.MIME != "image/png" {
		t.Fatal(a, err)
	}
	a, err = attachment.Prepare("project", "需求.md", []byte("# 附件正文\n最大金额 128 元"))
	if err != nil || !strings.Contains(a.Text, "128") || a.Kind != "document" {
		t.Fatal(a, err)
	}
	long := strings.Repeat("你", attachment.MaxTextRunes+1)
	a, err = attachment.Prepare("project", "long.txt", []byte(long))
	if err != nil || !a.Truncated || len([]rune(a.Text)) != attachment.MaxTextRunes {
		t.Fatal("text truncation failed", err)
	}
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"empty.txt", nil}, {"binary.bin", []byte{0, 1, 2, 3}}, {"bad.pdf", []byte("not a PDF")},
		{"broken.png", pngFile.Bytes()[:40]},
		{"large.txt", bytes.Repeat([]byte("x"), attachment.MaxSize+1)},
	} {
		if _, err = attachment.Prepare("project", tc.name, tc.body); err == nil {
			t.Fatalf("accepted %s", tc.name)
		}
	}
}

func TestDOCXAndPDFExtractActualText(t *testing.T) {
	var document bytes.Buffer
	z := zip.NewWriter(&document)
	w, err := z.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(w, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>需求说明：金额上限 520。</w:t></w:r></w:p><w:p><w:r><w:t>第二段内容。</w:t></w:r></w:p></w:body></w:document>`)
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := attachment.Prepare("project", "spec.docx", document.Bytes())
	if err != nil || !strings.Contains(a.Text, "520。\n第二段") {
		t.Fatal(a.Text, err)
	}
	var file bytes.Buffer
	fmt.Fprint(&file, "%PDF-1.4\n")
	stream := "BT /F1 12 Tf 50 700 Td (Refund limit 42) Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, file.Len())
		fmt.Fprintf(&file, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := file.Len()
	fmt.Fprint(&file, "xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&file, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&file, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	a, err = attachment.Prepare("project", "spec.pdf", file.Bytes())
	if err != nil || !strings.Contains(a.Text, "Refund limit 42") {
		t.Fatal(a.Text, err)
	}
}
