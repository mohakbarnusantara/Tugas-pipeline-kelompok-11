package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPasswordHash(t *testing.T) {
	hash := hashPassword("contoh-kata-sandi")
	if !verifyPassword(hash, "contoh-kata-sandi") {
		t.Fatal("kata sandi yang benar tidak diterima")
	}
	if verifyPassword(hash, "kata-sandi-lain") {
		t.Fatal("kata sandi yang salah diterima")
	}
}

func TestAllServicesHaveUniqueCodes(t *testing.T) {
	if len(services) != 30 {
		t.Fatalf("jumlah jenis surat = %d, ingin 30", len(services))
	}
	seen := make(map[string]bool, len(services))
	for _, service := range services {
		if service.Code == "" || service.Name == "" || len(service.Fields) == 0 {
			t.Errorf("spesifikasi layanan tidak lengkap: %#v", service)
		}
		if seen[service.Code] {
			t.Errorf("kode layanan duplikat: %s", service.Code)
		}
		seen[service.Code] = true
	}
	if len(complaintCategories) != 12 {
		t.Fatalf("jumlah kategori aduan = %d, ingin 12", len(complaintCategories))
	}
}

func TestStorePersistsUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "data.json")
	store, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	user := User{ID: "warga-1", Email: "warga@example.test", Role: "warga", CreatedAt: time.Now()}
	if err := store.update(func(db *database) error {
		db.Users[user.ID] = user
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.snapshot().Users[user.ID]; got.Email != user.Email {
		t.Fatalf("pengguna tidak tersimpan: %#v", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded database
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("berkas data tidak valid JSON: %v", err)
	}
}

func TestSessionSignatureAndExpiration(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	user := User{ID: "citizen-1", Email: "warga@example.test", Role: "warga", AccountStatus: "aktif"}
	if err := store.update(func(db *database) error {
		db.Users[user.ID] = user
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, secret: []byte("test-secret")}
	response := httptest.NewRecorder()
	if err := app.setSession(response, user.ID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/dashboard", nil)
	request.AddCookie(response.Result().Cookies()[0])
	if got := app.currentUser(request); got == nil || got.ID != user.ID {
		t.Fatal("sesi valid tidak ditemukan")
	}
	request.Header.Set("Cookie", sessionName+"=tampered.signature")
	if got := app.currentUser(request); got != nil {
		t.Fatal("sesi dengan tanda tangan tidak valid diterima")
	}
}

func TestSeedCreatesScopedStaffAndMayorAccounts(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := seedDemoUsers(store); err != nil {
		t.Fatal(err)
	}
	users := store.snapshot().Users
	if len(users) != 3+2*len(letterOffices) {
		t.Fatalf("jumlah akun = %d, ingin %d", len(users), 3+2*len(letterOffices))
	}
	for _, office := range letterOffices {
		for _, role := range []string{"staf", "lurah"} {
			account, ok := users[officeAccountID(role, office)]
			if !ok {
				t.Fatalf("akun %s untuk %s tidak dibuat", role, office.Neighborhood)
			}
			if account.Role != role || account.OfficeDistrict != office.District ||
				account.OfficeNeighborhood != office.Neighborhood || account.AccountStatus != "aktif" ||
				account.Email != officeAccountEmail(role, office) || account.PasswordHash == "" {
				t.Errorf("akun %s tidak memiliki scope yang benar: %#v", role, account)
			}
		}
	}
	if !verifyPassword(users[officeAccountID("staf", letterOffices[0])].PasswordHash, "silayang123") {
		t.Fatal("password demo akun kelurahan tidak sesuai")
	}
	for _, id := range []string{"demo-staf", "demo-lurah"} {
		if user := users[id]; user.OfficeDistrict != "Mantikulore" || user.OfficeNeighborhood != "Talise" {
			t.Errorf("akun lama %s tidak dibatasi ke Talise: %#v", id, user)
		}
	}
}

func TestSeedMapsLegacyLetterOnlyWhenAddressNamesKelurahan(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	requests := []SuratRequest{
		{ID: "known", Applicant: map[string]string{"alamat": "Jl. Mawar, Kel. Talise, Palu"}},
		{ID: "unknown", Applicant: map[string]string{"alamat": "Jalan Mawar, Kota Palu"}},
	}
	if err := store.update(func(db *database) error {
		db.Users["resident"] = User{ID: "resident", Role: "warga"}
		db.Users["known-resident"] = User{
			ID: "known-resident", Role: "warga", Address: "Jl. Mawar, Kel. Talise, Palu",
		}
		db.Users["unknown-resident"] = User{
			ID: "unknown-resident", Role: "warga", Address: "Jalan Mawar, Kota Palu",
		}
		for _, request := range requests {
			db.Requests[request.ID] = request
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := seedDemoUsers(store); err != nil {
		t.Fatal(err)
	}
	snapshot := store.snapshot()
	if got := snapshot.Requests["known"]; got.District != "Mantikulore" || got.Neighborhood != "Talise" {
		t.Errorf("surat lama yang punya kelurahan eksplisit tidak dipetakan: %#v", got)
	}
	if got := snapshot.Requests["unknown"]; got.District != "" || got.Neighborhood != "" {
		t.Errorf("surat tanpa kelurahan eksplisit dipetakan secara spekulatif: %#v", got)
	}
	if got := snapshot.Users["known-resident"]; got.HomeDistrict != "Mantikulore" || got.HomeNeighborhood != "Talise" {
		t.Errorf("domisili warga lama yang menyebut kelurahan tidak dipetakan: %#v", got)
	}
	if got := snapshot.Users["unknown-resident"]; got.HomeDistrict != "" || got.HomeNeighborhood != "" {
		t.Errorf("domisili tanpa kelurahan eksplisit dipetakan secara spekulatif: %#v", got)
	}
}

func TestRegisterStoresSelectedHomeOffice(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, secret: []byte("test-secret")}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{
		"name": "Warga Tatura", "nik": "1234567890123456", "email": "tatura@example.test",
		"phone": "081234567890", "birth_date": "1990-01-01", "gender": "L",
		"address": "Jl. Anoa", "home_district": "Palu Selatan",
		"home_neighborhood": "Tatura Utara", "password": "rahasia123",
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/register", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()

	app.register(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("pendaftaran berstatus %d, ingin 303: %s", response.Code, response.Body.String())
	}
	var resident User
	for _, user := range store.snapshot().Users {
		if user.Email == fields["email"] {
			resident = user
			break
		}
	}
	if resident.ID == "" || resident.HomeDistrict != "Palu Selatan" || resident.HomeNeighborhood != "Tatura Utara" {
		t.Fatalf("domisili pilihan tidak tersimpan: %#v", resident)
	}
}

func TestDashboardPendingNoticeUsesLatestAccountStatus(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	users := []User{
		{ID: "pending-resident", Role: "warga", AccountStatus: "pending"},
		{ID: "verified-resident", Role: "warga", AccountStatus: "aktif"},
	}
	if err := store.update(func(db *database) error {
		for _, user := range users {
			db.Users[user.ID] = user
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := &App{
		store: store, secret: []byte("test-secret"),
		templates: template.Must(template.New("dashboard").Parse(
			`{{define "dashboard"}}{{.Notice}}{{end}}`,
		)),
	}
	for _, testCase := range []struct {
		userID  string
		want    string
		notWant string
	}{
		{
			userID: "pending-resident",
			want:   "Akun Anda menunggu verifikasi staf kelurahan",
		},
		{
			userID:  "verified-resident",
			want:    "Akun Anda sudah diverifikasi",
			notWant: "menunggu verifikasi",
		},
	} {
		cookieResponse := httptest.NewRecorder()
		if err := app.setSession(cookieResponse, testCase.userID); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("GET", "/dashboard?notice=pending", nil)
		request.AddCookie(cookieResponse.Result().Cookies()[0])
		response := httptest.NewRecorder()
		app.dashboard(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), testCase.want) {
			t.Errorf("pesan untuk %s tidak sesuai (%d): %q", testCase.userID, response.Code, response.Body.String())
		}
		if testCase.notWant != "" && strings.Contains(response.Body.String(), testCase.notWant) {
			t.Errorf("pesan lama masih terlihat untuk %s: %q", testCase.userID, response.Body.String())
		}
	}
}

func TestDashboardShowsPrintLinkOnlyForCompletedLetters(t *testing.T) {
	templates, err := template.New("dashboard").Funcs(template.FuncMap{
		"statusClass": func(string) string { return "success" },
		"statusName":  func(status string) string { return status },
		"date":        func(value time.Time) string { return value.Format("02 Jan 2006, 15:04") },
	}).ParseFiles(filepath.Join("templates", "dashboard.html"))
	if err != nil {
		t.Fatal(err)
	}
	data := PageData{
		User: &User{Role: "warga", AccountStatus: "aktif"},
		Requests: []SuratRequest{
			{ID: "completed-letter", ServiceName: "Surat Selesai", TrackingCode: "SK-001", Status: "selesai"},
			{ID: "pending-letter", ServiceName: "Surat Menunggu", TrackingCode: "SK-002", Status: "pending_verifikasi"},
		},
	}
	var output bytes.Buffer
	if err := templates.ExecuteTemplate(&output, "dashboard-content", data); err != nil {
		t.Fatal(err)
	}
	rendered := output.String()
	if !strings.Contains(rendered, `href="/dokumen/completed-letter"`) ||
		!strings.Contains(rendered, "Cetak / Simpan PDF") {
		t.Fatalf("surat selesai tidak memiliki tautan cetak: %s", rendered)
	}
	if strings.Contains(rendered, `href="/dokumen/pending-letter"`) {
		t.Fatal("tautan cetak tampil sebelum surat selesai")
	}
}

func TestStaffResidentVerificationAndKTPAreOfficeScoped(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	staff := User{
		ID: "staff-tondo", Role: "staf", AccountStatus: "aktif",
		OfficeDistrict: "Mantikulore", OfficeNeighborhood: "Tondo",
	}
	ownResident := User{
		ID: "resident-tondo", Name: "Warga Tondo", Role: "warga", AccountStatus: "pending",
		HomeDistrict: "Mantikulore", HomeNeighborhood: "Tondo", KTPPath: "/uploads/tondo.png",
	}
	foreignResident := User{
		ID: "resident-tatura", Name: "Warga Tatura Utara", Role: "warga", AccountStatus: "pending",
		HomeDistrict: "Palu Selatan", HomeNeighborhood: "Tatura Utara", KTPPath: "/uploads/tatura.png",
	}
	if err := store.update(func(db *database) error {
		db.Users[staff.ID] = staff
		db.Users[ownResident.ID] = ownResident
		db.Users[foreignResident.ID] = foreignResident
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	uploadDir := t.TempDir()
	for _, name := range []string{"tondo.png", "tatura.png"} {
		if err := os.WriteFile(filepath.Join(uploadDir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{
		store: store, secret: []byte("test-secret"), uploadDir: uploadDir,
		templates: template.Must(template.New("dashboard").Parse(
			`{{define "admin"}}{{range .Residents}}{{.ID}}|{{end}}{{end}}{{define "dashboard"}}{{.Stats.Residents}}{{end}}`,
		)),
	}
	cookieResponse := httptest.NewRecorder()
	if err := app.setSession(cookieResponse, staff.ID); err != nil {
		t.Fatal(err)
	}
	cookie := cookieResponse.Result().Cookies()[0]

	pageRequest := httptest.NewRequest("GET", "/admin", nil)
	pageRequest.AddCookie(cookie)
	pageResponse := httptest.NewRecorder()
	app.adminPage(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK || strings.Contains(pageResponse.Body.String(), foreignResident.ID) ||
		!strings.Contains(pageResponse.Body.String(), ownResident.ID) {
		t.Fatalf("daftar verifikasi tidak terbatasi ke Tondo: %d %q", pageResponse.Code, pageResponse.Body.String())
	}
	dashboardRequest := httptest.NewRequest("GET", "/dashboard", nil)
	dashboardRequest.AddCookie(cookie)
	dashboardResponse := httptest.NewRecorder()
	app.dashboard(dashboardResponse, dashboardRequest)
	if dashboardResponse.Code != http.StatusOK || dashboardResponse.Body.String() != "1" {
		t.Fatalf("jumlah warga menunggu tidak terbatasi ke Tondo: %d %q", dashboardResponse.Code, dashboardResponse.Body.String())
	}

	reviewRequest := httptest.NewRequest("POST", "/admin/warga/"+foreignResident.ID,
		strings.NewReader("action=approve"))
	reviewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reviewRequest.AddCookie(cookie)
	reviewRequest.SetPathValue("id", foreignResident.ID)
	reviewResponse := httptest.NewRecorder()
	app.reviewResident(reviewResponse, reviewRequest)
	if reviewResponse.Code != http.StatusBadRequest {
		t.Fatalf("verifikasi warga kelurahan lain berstatus %d, ingin ditolak", reviewResponse.Code)
	}
	if got := store.snapshot().Users[foreignResident.ID].AccountStatus; got != "pending" {
		t.Fatalf("status warga kelurahan lain berubah menjadi %q", got)
	}

	ownReviewRequest := httptest.NewRequest("POST", "/admin/warga/"+ownResident.ID,
		strings.NewReader("action=approve"))
	ownReviewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ownReviewRequest.AddCookie(cookie)
	ownReviewRequest.SetPathValue("id", ownResident.ID)
	ownReviewResponse := httptest.NewRecorder()
	app.reviewResident(ownReviewResponse, ownReviewRequest)
	if ownReviewResponse.Code != http.StatusSeeOther {
		t.Fatalf("verifikasi warga Tondo berstatus %d, ingin 303", ownReviewResponse.Code)
	}

	foreignKTPRequest := httptest.NewRequest("GET", "/uploads/tatura.png", nil)
	foreignKTPRequest.AddCookie(cookie)
	foreignKTPRequest.SetPathValue("name", "tatura.png")
	foreignKTPResponse := httptest.NewRecorder()
	app.upload(foreignKTPResponse, foreignKTPRequest)
	if foreignKTPResponse.Code != http.StatusForbidden {
		t.Fatalf("akses KTP kelurahan lain berstatus %d, ingin 403", foreignKTPResponse.Code)
	}

	ownKTPRequest := httptest.NewRequest("GET", "/uploads/tondo.png", nil)
	ownKTPRequest.AddCookie(cookie)
	ownKTPRequest.SetPathValue("name", "tondo.png")
	ownKTPResponse := httptest.NewRecorder()
	app.upload(ownKTPResponse, ownKTPRequest)
	if ownKTPResponse.Code != http.StatusOK || ownKTPResponse.Body.String() != "tondo.png" {
		t.Fatalf("akses KTP Tondo berstatus %d: %q", ownKTPResponse.Code, ownKTPResponse.Body.String())
	}
}

func TestOfficeStaffCannotReviewViewOrDownloadAnotherOfficeLetter(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	staff := User{
		ID: "staff-tondo", Role: "staf", AccountStatus: "aktif",
		OfficeDistrict: "Mantikulore", OfficeNeighborhood: "Tondo",
	}
	foreignRequest := SuratRequest{
		ID: "letter-talise", UserID: "resident-1", District: "Mantikulore",
		Neighborhood: "Talise", Status: "selesai", Attachments: []string{"/uploads/private.pdf"},
	}
	ownRequest := SuratRequest{
		ID: "letter-tondo", UserID: "resident-2", District: "Mantikulore",
		Neighborhood: "Tondo", Status: "pending_verifikasi",
	}
	if err := store.update(func(db *database) error {
		db.Users[staff.ID] = staff
		db.Requests[foreignRequest.ID] = foreignRequest
		db.Requests[ownRequest.ID] = ownRequest
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, secret: []byte("test-secret"), uploadDir: t.TempDir()}
	cookieResponse := httptest.NewRecorder()
	if err := app.setSession(cookieResponse, staff.ID); err != nil {
		t.Fatal(err)
	}
	cookie := cookieResponse.Result().Cookies()[0]

	reviewRequest := httptest.NewRequest("POST", "/admin/surat/letter-talise",
		strings.NewReader("action=draft&official_number=TEST-001"))
	reviewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reviewRequest.AddCookie(cookie)
	reviewRequest.SetPathValue("id", foreignRequest.ID)
	reviewResponse := httptest.NewRecorder()
	app.reviewSurat(reviewResponse, reviewRequest)
	if reviewResponse.Code != http.StatusBadRequest {
		t.Fatalf("aksi surat lintas kelurahan berstatus %d, ingin ditolak", reviewResponse.Code)
	}
	if got := store.snapshot().Requests[foreignRequest.ID].Status; got != "selesai" {
		t.Fatalf("status surat kelurahan lain berubah menjadi %q", got)
	}

	ownReviewRequest := httptest.NewRequest("POST", "/admin/surat/letter-tondo",
		strings.NewReader("action=draft&official_number=TONDO-001"))
	ownReviewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ownReviewRequest.AddCookie(cookie)
	ownReviewRequest.SetPathValue("id", ownRequest.ID)
	ownReviewResponse := httptest.NewRecorder()
	app.reviewSurat(ownReviewResponse, ownReviewRequest)
	if ownReviewResponse.Code != http.StatusSeeOther {
		t.Fatalf("aksi surat kelurahan sendiri berstatus %d, ingin 303: %s", ownReviewResponse.Code, ownReviewResponse.Body.String())
	}
	if got := store.snapshot().Requests[ownRequest.ID].Status; got != "draft_ready" {
		t.Fatalf("status surat kelurahan sendiri = %q, ingin draft_ready", got)
	}

	documentRequest := httptest.NewRequest("GET", "/dokumen/letter-talise", nil)
	documentRequest.AddCookie(cookie)
	documentRequest.SetPathValue("id", foreignRequest.ID)
	documentResponse := httptest.NewRecorder()
	app.document(documentResponse, documentRequest)
	if documentResponse.Code != http.StatusNotFound {
		t.Fatalf("dokumen kelurahan lain berstatus %d, ingin 404", documentResponse.Code)
	}

	uploadRequest := httptest.NewRequest("GET", "/uploads/private.pdf", nil)
	uploadRequest.AddCookie(cookie)
	uploadRequest.SetPathValue("name", "private.pdf")
	uploadResponse := httptest.NewRecorder()
	app.upload(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusForbidden {
		t.Fatalf("lampiran kelurahan lain berstatus %d, ingin 403", uploadResponse.Code)
	}
}

func TestApprovedDocumentShowsDummyQRAndNIPWithoutVerificationCode(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	office, ok := letterOffice("Palu Selatan", "Tatura Utara")
	if !ok {
		t.Fatal("kantor Tatura Utara tidak ditemukan")
	}
	resident := User{ID: "resident-1", Role: "warga", AccountStatus: "aktif"}
	request := SuratRequest{
		ID: "approved-letter", TrackingCode: "SK-TEST-001", UserID: "resident-1",
		ServiceName: "Surat Keterangan", District: office.District, Neighborhood: office.Neighborhood,
		Applicant: map[string]string{
			"nama": "Warga Contoh", "nik": "1234567890123456", "tanggal_lahir": "Palu, 01 Januari 2000",
			"jenis_kelamin": "Laki-laki", "alamat": "Jl. Anoa",
		},
		OfficialNumber: "001/2026", Status: "selesai", UpdatedAt: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
	}
	if err := store.update(func(db *database) error {
		db.Users[resident.ID] = resident
		db.Requests[request.ID] = request
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	templates, err := template.New("document").Funcs(template.FuncMap{
		"date": func(value time.Time) string { return value.Format("02 Jan 2006, 15:04") },
	}).ParseFiles(filepath.Join("templates", "document.html"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, secret: []byte("test-secret"), templates: templates}
	cookieResponse := httptest.NewRecorder()
	if err := app.setSession(cookieResponse, resident.ID); err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest("GET", "/dokumen/"+request.ID, nil)
	httpRequest.AddCookie(cookieResponse.Result().Cookies()[0])
	httpRequest.SetPathValue("id", request.ID)
	response := httptest.NewRecorder()

	app.document(response, httpRequest)

	output := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("dokumen berstatus %d: %s", response.Code, output)
	}
	for _, expected := range []string{
		"QR SIMULASI", "NIP. 000000000000000000 (DUMMY)", office.Mayor,
		"tidak dapat digunakan untuk verifikasi",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("dokumen tidak menampilkan %q", expected)
		}
	}
	for _, removed := range []string{"Kode verifikasi:", "TTE DIGITAL (SIMULASI)", "qr_hash"} {
		if strings.Contains(output, removed) {
			t.Errorf("dokumen masih menampilkan %q", removed)
		}
	}
}

func TestDraftPreviewUsesVerifiedUserAndSelectedLocation(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	user := User{
		ID: "citizen-1", Name: "Contoh Warga", NIK: "1234567890123456", BirthPlace: "Palu",
		BirthDate: "1990-01-02", Gender: "P", Occupation: "Guru", Address: "Jalan Contoh",
		Role: "warga", AccountStatus: "aktif",
	}
	mayor := User{ID: "mayor-1", Name: "Contoh Lurah", Role: "lurah"}
	if err := store.update(func(db *database) error {
		db.Users[user.ID] = user
		db.Users[mayor.ID] = mayor
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, secret: []byte("test-secret")}
	cookieResponse := httptest.NewRecorder()
	if err := app.setSession(cookieResponse, user.ID); err != nil {
		t.Fatal(err)
	}
	body := `{"user_id":"citizen-1","jenis_surat_id":"SK-AHLI-WARIS","data_tambahan":{"kecamatan":"Mantikulore","kelurahan":"Talise","keperluan":"Administrasi keluarga","specific_hubungan_keluarga":"anak"}}`
	request := httptest.NewRequest("POST", "/api/v1/letters/draft-preview", strings.NewReader(body))
	request.AddCookie(cookieResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()

	app.draftPreview(response, request)

	if response.Code != 200 {
		t.Fatalf("status pratinjau = %d, ingin 200: %s", response.Code, response.Body.String())
	}
	var result DraftPreviewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.DraftData == nil {
		t.Fatalf("respons pratinjau tidak berhasil: %#v", result)
	}
	draft := result.DraftData
	if draft.District != "Mantikulore" || draft.Neighborhood != "Talise" ||
		draft.OfficeName != "Kantor Kelurahan Talise" ||
		!strings.Contains(draft.Header, "KELURAHAN TALISE") {
		t.Errorf("kop tidak memakai wilayah pilihan: %#v", draft)
	}
	if draft.OfficeAddress != "Jl. Letjend Suprapto No.11, Talise, Kec. Mantikulore, Kota Palu, Sulawesi Tengah 94118" ||
		!strings.Contains(draft.Header, draft.OfficeAddress) {
		t.Errorf("kop tidak memakai alamat kantor dari data wilayah: %#v", draft)
	}
	if draft.FullName != user.Name || draft.NIK != user.NIK || draft.Birth != "Palu, 02 Januari 1990" {
		t.Errorf("data profil tidak terpetakan: %#v", draft)
	}
	if draft.Purpose != "Administrasi keluarga" || draft.Occupation != "Guru" ||
		draft.OfficialNumber != "" || draft.MayorName != "Mohamad Iqbal, S.H., M.H" || !strings.Contains(draft.MayorNIP, "DUMMY") {
		t.Errorf("field draf tidak sesuai: %#v", draft)
	}
	if len(draft.AdditionalFields) != 1 || draft.AdditionalFields[0].Label != "Hubungan keluarga" {
		t.Errorf("data tambahan surat tidak terpetakan: %#v", draft.AdditionalFields)
	}
}

func TestLetterOfficeDirectoryHasUniqueAreas(t *testing.T) {
	if len(letterOffices) != 32 {
		t.Fatalf("jumlah kantor kelurahan = %d, ingin 32 dari spreadsheet", len(letterOffices))
	}
	seen := make(map[string]bool, len(letterOffices))
	for _, office := range letterOffices {
		key := office.District + "/" + office.Neighborhood
		if office.District == "" || office.Neighborhood == "" || office.Name == "" || office.Mayor == "" || office.Address == "" {
			t.Errorf("data kantor tidak lengkap: %#v", office)
		}
		if seen[key] {
			t.Errorf("pasangan kecamatan/kelurahan duplikat: %s", key)
		}
		seen[key] = true
	}
	tondo, ok := letterOffice("Mantikulore", "Tondo")
	if !ok || tondo.Address != "Jl. Soekarno-Hatta No. 1, Kota Palu, Sulawesi Tengah 94118" ||
		officeLetterEmail(tondo) != "tondo@palukota.go.id" {
		t.Errorf("alamat atau email Tondo tidak mengikuti spesifikasi kop: %#v", tondo)
	}
}

func TestDraftPreviewRejectsUnverifiedAndMismatchedAccounts(t *testing.T) {
	store, err := openStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	user := User{ID: "citizen-1", Role: "warga", AccountStatus: "pending"}
	if err := store.update(func(db *database) error {
		db.Users[user.ID] = user
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app := &App{store: store, secret: []byte("test-secret")}
	cookieResponse := httptest.NewRecorder()
	if err := app.setSession(cookieResponse, user.ID); err != nil {
		t.Fatal(err)
	}
	body := `{"user_id":"another-user","jenis_surat_id":"SK-AHLI-WARIS","data_tambahan":{}}`
	request := httptest.NewRequest("POST", "/api/v1/letters/draft-preview", strings.NewReader(body))
	request.AddCookie(cookieResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()

	app.draftPreview(response, request)

	if response.Code != 403 {
		t.Fatalf("status akun belum terverifikasi = %d, ingin 403", response.Code)
	}

	if err := store.update(func(db *database) error {
		current := db.Users[user.ID]
		current.AccountStatus = "aktif"
		db.Users[user.ID] = current
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mismatchedBody := `{"user_id":"another-user","jenis_surat_id":"SK-AHLI-WARIS","data_tambahan":{}}`
	mismatchedRequest := httptest.NewRequest("POST", "/api/v1/letters/draft-preview", strings.NewReader(mismatchedBody))
	mismatchedRequest.AddCookie(cookieResponse.Result().Cookies()[0])
	mismatchedResponse := httptest.NewRecorder()
	app.draftPreview(mismatchedResponse, mismatchedRequest)
	if mismatchedResponse.Code != 403 {
		t.Fatalf("status ID pengguna yang tidak sesuai = %d, ingin 403", mismatchedResponse.Code)
	}
}
