package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxUploadSize = 5 << 20
	sessionName   = "silayang_session"
)

type User struct {
	ID                 string    `json:"id"`
	NIK                string    `json:"nik"`
	Name               string    `json:"name"`
	Email              string    `json:"email"`
	Phone              string    `json:"phone"`
	BirthPlace         string    `json:"birth_place,omitempty"`
	BirthDate          string    `json:"birth_date"`
	Gender             string    `json:"gender"`
	Address            string    `json:"address"`
	Occupation         string    `json:"occupation,omitempty"`
	KTPPath            string    `json:"ktp_path,omitempty"`
	HomeDistrict       string    `json:"home_district,omitempty"`
	HomeNeighborhood   string    `json:"home_neighborhood,omitempty"`
	Role               string    `json:"role"`
	AccountStatus      string    `json:"account_status"`
	OfficeDistrict     string    `json:"office_district,omitempty"`
	OfficeNeighborhood string    `json:"office_neighborhood,omitempty"`
	RejectionReason    string    `json:"rejection_reason,omitempty"`
	PasswordHash       string    `json:"password_hash"`
	CreatedAt          time.Time `json:"created_at"`
}

type Service struct {
	Code     string
	Category string
	Name     string
	Fields   []string
	ExtraDoc string
}

type SuratRequest struct {
	ID             string            `json:"id"`
	TrackingCode   string            `json:"tracking_code"`
	UserID         string            `json:"user_id"`
	ServiceCode    string            `json:"service_code"`
	ServiceName    string            `json:"service_name"`
	District       string            `json:"kecamatan,omitempty"`
	Neighborhood   string            `json:"kelurahan,omitempty"`
	IsSelf         bool              `json:"is_self"`
	Applicant      map[string]string `json:"applicant"`
	Specific       map[string]string `json:"specific"`
	Purpose        string            `json:"keperluan,omitempty"`
	RTLetterNumber string            `json:"rt_letter_number"`
	Attachments    []string          `json:"attachments,omitempty"`
	OfficialNumber string            `json:"official_number,omitempty"`
	Status         string            `json:"status"`
	AdminNote      string            `json:"admin_note,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type Complaint struct {
	ID           string    `json:"id"`
	TrackingCode string    `json:"tracking_code"`
	UserID       string    `json:"user_id"`
	Category     string    `json:"category"`
	District     string    `json:"district"`
	Neighborhood string    `json:"neighborhood"`
	Description  string    `json:"description"`
	EvidencePath string    `json:"evidence_path,omitempty"`
	Status       string    `json:"status"`
	AdminReply   string    `json:"admin_reply,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type database struct {
	Users      map[string]User         `json:"users"`
	Requests   map[string]SuratRequest `json:"requests"`
	Complaints map[string]Complaint    `json:"complaints"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data database
}

func openStore(path string) (*Store, error) {
	s := &Store{path: path, data: database{
		Users: map[string]User{}, Requests: map[string]SuratRequest{}, Complaints: map[string]Complaint{},
	}}
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("membaca basis data: %w", err)
		}
		s.ensureMaps()
		return s, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("membuka basis data: %w", err)
	}
	return s, nil
}

func (s *Store) ensureMaps() {
	if s.data.Users == nil {
		s.data.Users = map[string]User{}
	}
	if s.data.Requests == nil {
		s.data.Requests = map[string]SuratRequest{}
	}
	if s.data.Complaints == nil {
		s.data.Complaints = map[string]Complaint{}
	}
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("membuat direktori data: %w", err)
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("menyandikan basis data: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "silayang-*.tmp")
	if err != nil {
		return fmt.Errorf("membuat berkas basis data sementara: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("mengatur izin basis data: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("menulis basis data: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("menutup basis data: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("menyimpan basis data: %w", err)
	}
	return nil
}

func (s *Store) update(fn func(*database) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.data); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *Store) snapshot() database {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copyData := database{
		Users:      make(map[string]User, len(s.data.Users)),
		Requests:   make(map[string]SuratRequest, len(s.data.Requests)),
		Complaints: make(map[string]Complaint, len(s.data.Complaints)),
	}
	for k, v := range s.data.Users {
		copyData.Users[k] = v
	}
	for k, v := range s.data.Requests {
		copyData.Requests[k] = v
	}
	for k, v := range s.data.Complaints {
		copyData.Complaints[k] = v
	}
	return copyData
}

type App struct {
	store        *Store
	templates    *template.Template
	secret       []byte
	uploadDir    string
	secureCookie bool
}

type PageData struct {
	Title         string
	Page          string
	User          *User
	Error         string
	Notice        string
	Services      []Service
	Categories    []string
	Requests      []SuratRequest
	Complaints    []Complaint
	Residents     []User
	Request       *SuratRequest
	Complaint     *Complaint
	Stats         Stats
	Code          string
	Districts     []string
	Neighborhoods map[string][]string
}

type LetterOffice struct {
	Neighborhood string `json:"kelurahan"`
	District     string `json:"kecamatan"`
	Name         string `json:"nama_tempat"`
	Mayor        string `json:"nama_lurah"`
	Address      string `json:"alamat"`
}

type DraftPreviewRequest struct {
	UserID      string            `json:"user_id"`
	ServiceCode string            `json:"jenis_surat_id"`
	Additional  map[string]string `json:"data_tambahan"`
}

type DraftField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type DraftData struct {
	LetterName       string       `json:"jenis_surat"`
	Header           string       `json:"kop_kelurahan"`
	District         string       `json:"kecamatan"`
	Neighborhood     string       `json:"kelurahan"`
	OfficeName       string       `json:"nama_kantor"`
	OfficeAddress    string       `json:"alamat_kantor"`
	OfficeEmail      string       `json:"email_kantor,omitempty"`
	FullName         string       `json:"nama_lengkap"`
	NIK              string       `json:"nik"`
	Birth            string       `json:"tempat_tgl_lahir"`
	Gender           string       `json:"jenis_kelamin"`
	Occupation       string       `json:"pekerjaan"`
	Address          string       `json:"alamat_lengkap"`
	Purpose          string       `json:"keperluan"`
	OfficialNumber   string       `json:"nomor_surat"`
	DraftDate        string       `json:"tanggal_draf"`
	MayorName        string       `json:"nama_lurah"`
	MayorNIP         string       `json:"nip_lurah"`
	AdditionalFields []DraftField `json:"data_tambahan"`
}

type DraftPreviewResponse struct {
	Status    string     `json:"status"`
	Message   string     `json:"message,omitempty"`
	DraftData *DraftData `json:"draft_data,omitempty"`
}

type Stats struct {
	Residents  int
	Pending    int
	Processing int
	Completed  int
	Complaints int
}

var services = []Service{
	{"SK-AHLI-WARIS", "Kematian & Bencana", "Surat Keterangan Ahli Waris", []string{"Hubungan keluarga", "Nama almarhum/ah", "Tanggal meninggal"}, ""},
	{"SK-BEDA-NAMA", "Kematian & Bencana", "Surat Keterangan Beda Nama", []string{"Nama berbeda", "NIK berbeda", "TTL berbeda", "Alamat berbeda"}, ""},
	{"SK-BEPERGIAN", "Kematian & Bencana", "Surat Keterangan Bepergian", []string{"Maksud bepergian", "Tujuan bepergian", "Lama bepergian", "Jenis transportasi"}, ""},
	{"SK-BERKELAKUAN-BAIK", "Kematian & Bencana", "Surat Keterangan Berkelakuan Baik", []string{"Status perkawinan", "Maksud dan tujuan", "No. HP/WA"}, "Foto KTP"},
	{"SK-HILANG-KEBERADAAN", "Kematian & Bencana", "Surat Keterangan Hilang Keberadaan", []string{"Status perkawinan"}, ""},
	{"SK-KEMATIAN", "Kematian & Bencana", "Surat Keterangan Kematian", []string{"Tanggal kematian", "Jam kematian", "Tempat kematian", "Sebab kematian", "Nama pelapor", "Hubungan keluarga"}, ""},
	{"SK-PENGUBURAN", "Kematian & Bencana", "Surat Keterangan Penguburan", []string{"Tanggal meninggal", "Tanggal pemakaman", "Tempat pemakaman"}, ""},
	{"SK-SAKIT-JIWA", "Kematian & Bencana", "Surat Keterangan Sakit Jiwa", []string{"Status perkawinan", "RT/RW"}, ""},
	{"SK-TERDAMPAK-BENCANA", "Kematian & Bencana", "Surat Keterangan Terdampak Bencana", []string{"Status perkawinan", "Nama bencana", "Lokasi berkumpul saat bencana"}, ""},
	{"SK-BELUM-BEKERJA", "Pekerjaan & Kependudukan", "Surat Keterangan Belum Bekerja", []string{"RT/RW"}, ""},
	{"SK-BELUM-PUNYA-KK", "Pekerjaan & Kependudukan", "Surat Keterangan Belum Memiliki KK", []string{"Kewarganegaraan", "Pekerjaan", "RT/RW"}, ""},
	{"SK-BELUM-PUNYA-RUMAH", "Pekerjaan & Kependudukan", "Surat Keterangan Belum Memiliki Rumah", []string{"Status perkawinan", "Pekerjaan"}, ""},
	{"SK-TIDAK-BEKERJA-LAGI", "Pekerjaan & Kependudukan", "Surat Keterangan Tidak Bekerja Lagi", []string{"Status perkawinan", "RT/RW"}, ""},
	{"SK-PENERBITAN-SPPT", "Pekerjaan & Kependudukan", "Surat Keterangan Penerbitan SPPT", []string{"Subjek objek pajak", "No. surat keterangan penguasaan", "Luas tanah", "Letak objek pajak", "No. SPPT lama"}, ""},
	{"SK-AKSES-JALAN", "Domisili & Tempat Tinggal", "Surat Keterangan Akses Jalan", []string{"No. sertifikat", "Luas tanah", "Panjang", "Lebar"}, ""},
	{"SK-DOMISILI-KANTOR", "Domisili & Tempat Tinggal", "Surat Keterangan Domisili Kantor", []string{"Nama kantor", "Nama pimpinan", "Alamat kantor", "RT/RW"}, ""},
	{"SK-DOMISILI-USAHA", "Domisili & Tempat Tinggal", "Surat Keterangan Domisili Usaha", []string{"Nama usaha", "Jenis usaha", "Alamat usaha", "Status tempat usaha", "Jumlah karyawan", "Luas tempat usaha"}, ""},
	{"SK-DOMISILI-YAYASAN", "Domisili & Tempat Tinggal", "Surat Keterangan Domisili Yayasan/Kesekretariatan", []string{"Nama yayasan", "Nama pimpinan", "Alamat yayasan", "RT/RW"}, ""},
	{"SK-TEMPAT-TINGGAL", "Domisili & Tempat Tinggal", "Surat Keterangan Tempat Tinggal", []string{"RT/RW"}, ""},
	{"SK-DUDA-CERAI-HIDUP", "Status Perkawinan & Keluarga", "Surat Keterangan Duda (cerai hidup)", []string{"Nama istri", "Tanggal cerai", "No. akta cerai"}, "Scan akta cerai"},
	{"SK-DUDA-CERAI-MATI", "Status Perkawinan & Keluarga", "Surat Keterangan Duda (cerai mati)", []string{"Nama istri", "Tanggal kematian istri"}, ""},
	{"SK-JANDA-CERAI-HIDUP", "Status Perkawinan & Keluarga", "Surat Keterangan Janda (cerai hidup)", []string{"Nama suami", "Tanggal cerai", "No. akta cerai"}, "Scan akta cerai"},
	{"SK-JANDA-CERAI-MATI", "Status Perkawinan & Keluarga", "Surat Keterangan Janda (cerai mati)", []string{"Nama suami", "Tanggal kematian suami"}, ""},
	{"SK-BELUM-MENIKAH", "Status Perkawinan & Keluarga", "Surat Keterangan Belum Menikah", []string{"Status perkawinan", "Pekerjaan"}, ""},
	{"SK-PENGANTAR-CATIN", "Status Perkawinan & Keluarga", "Surat Pengantar Catin (Pemeriksaan Health & Imunisasi)", []string{"Status perkawinan", "Pekerjaan", "RT/RW"}, ""},
	{"SK-EKONOMI-LEMAH", "Usaha & Ekonomi", "Surat Keterangan Ekonomi Lemah", []string{"Tanggal SK penetapan (opsional)"}, ""},
	{"SK-EKONOMI-LEMAH-NON-DTKSP", "Usaha & Ekonomi", "Surat Keterangan Ekonomi Lemah (Belum DTKSP)", []string{"Status perkawinan", "Maksud dan tujuan"}, ""},
	{"SK-PENUTUPAN-USAHA", "Usaha & Ekonomi", "Surat Keterangan Penutupan Usaha", []string{"Jenis usaha", "Alamat usaha", "Tahun penutupan", "No. surat penutupan", "Tanggal surat penutupan"}, "Surat pengantar penutupan usaha"},
	{"SK-PENGHASILAN", "Usaha & Ekonomi", "Surat Keterangan Penghasilan", []string{"Data anak (nama, NIK, TTL, status, pekerjaan, alamat)", "Penghasilan rata-rata", "Sumber penghasilan"}, ""},
	{"SK-USAHA", "Usaha & Ekonomi", "Surat Keterangan Usaha", []string{"Nama usaha", "Tahun berdiri", "Nomor WA"}, ""},
}

var complaintCategories = []string{
	"Dana Stimulan/Huntap", "Drainase Rusak", "Infrastruktur", "Jalan Rusak",
	"Ketentraman dan Ketertiban Umum", "Lainnya", "Lampu Jalan", "Non Infrastruktur",
	"Pelayanan Publik", "Pungli, Gratifikasi, dan KKN", "Sampah", "Trans Palu",
}

var letterOffices = []LetterOffice{
	{"Kawatuna", "Mantikulore", "Kantor Kelurahan Kawatuna", "Mita Niaresvy Fazela, S.STP., MM (Plt. Lurah)", "Jl. Gn. Bulili, Kawatuna, Kec. Mantikulore, Kota Palu, Sulawesi Tengah 94111"},
	{"Lasoani", "Mantikulore", "Kantor Kelurahan Lasoani", "Erwin, S.H", "Jl. Bulu Masomba, Lasoani, Kec. Mantikulore, Kota Palu, Sulawesi Tengah 94118"},
	{"Layana Indah", "Mantikulore", "Kantor Kelurahan Layana Indah", "Sarlin, SE", "Jl. Layana Indah, Mantikulore, Palu City, Central Sulawesi 94147"},
	{"Poboya", "Mantikulore", "Kantor Kelurahan Poboya", "Muhamad Zulfin", "Jl. Tanamodindi, Kec. Palu Sel., Kota Palu, Sulawesi Tengah 94118"},
	{"Talise", "Mantikulore", "Kantor Kelurahan Talise", "Mohamad Iqbal, S.H., M.H", "Jl. Letjend Suprapto No.11, Talise, Kec. Mantikulore, Kota Palu, Sulawesi Tengah 94118"},
	{"Talise Valangguni", "Mantikulore", "Kantor Kelurahan Talise Valangguni", "Hasan Hamid, S.Sos", "Jl. Dayodara Lrg. Pakora 2 No.22, Besusu Tim., Kec. Palu Tim., Kota Palu, Sulawesi Tengah 94118"},
	{"Tanamodindi", "Mantikulore", "Kantor Kelurahan Tanamodindi", "Hamdan, S.Sos", "Jl. Veteran Lasoani, Jl. Veteran No.59, Tanamodindi, Kec. Palu Tim., Kota Palu, Sulawesi Tengah 94111"},
	{"Tondo", "Mantikulore", "Kantor Kelurahan Tondo", "Mursidin Siraj, S.Pd.I", "Jl. Soekarno-Hatta No. 1, Kota Palu, Sulawesi Tengah 94118"},
	{"Balaroa", "Palu Barat", "Kantor Kelurahan Balaroa", "Cristian, S.A.P", "Jl. Yambaere No.5, Balaroa, Kec. Palu Bar., Kota Palu, Sulawesi Tengah 94226"},
	{"Baru", "Palu Barat", "Kantor Kelurahan Baru", "Bachira Ulfah, S.Sos (Plh. Lurah)", "Jl. Teuku Umar, Baru, Kec. Palu Bar., Kota Palu, Sulawesi Tengah 94111"},
	{"Kamonji", "Palu Barat", "Kantor Kelurahan Kamonji", "Mohamad Rizal, S.A.P", "Jl. Datu Pamusu No.7, Kamonji, Kec. Palu Bar., Kota Palu, Sulawesi Tengah 94223"},
	{"Lere", "Palu Barat", "Kantor Kelurahan Lere", "Muhammad Fahmi, S.E | Megawati", "Jl. Pangeran Hidayat, Lere, Kec. Palu Bar., Kota Palu, Sulawesi Tengah 94221"},
	{"Siranindi", "Palu Barat", "Kantor Kelurahan Siranindi", "Sumarsih, SE., M.H", "Jl. Siranindi, Palu Barat, Palu City, Central Sulawesi 94111"},
	{"Ujuna", "Palu Barat", "Kantor Kelurahan Ujuna", "Reenas, S.Sos", "Jl. Sungai Wuno No.17, Ujuna, Kec. Palu Bar., Kota Palu, Sulawesi Tengah 94222"},
	{"Birobuli Selatan", "Palu Selatan", "Kantor Kelurahan Birobuli Selatan", "Irma, S.Sos", "Jl. Kijang Utara 2 No.7 Blok K, Birobuli Sel., Kec. Palu Sel., Kota Palu, Sulawesi Tengah 94235"},
	{"Birobuli Utara", "Palu Selatan", "Kantor Kelurahan Birobuli Utara", "Rini Suwarni, S.H., M.H", "Jl. Prof. Moh. Yamin No.2, Birobuli Utara, Kec. Palu Sel., Kota Palu, Sulawesi Tengah 94111"},
	{"Petobo", "Palu Selatan", "Kantor Kelurahan Petobo", "Alfin H. Ladjuni, S.Sos", "Jl. Moh. Soeharto, Petobo, Kec. Palu Sel., Kota Palu, Sulawesi Tengah 94234"},
	{"Tatura Selatan", "Palu Selatan", "Kantor Kelurahan Tatura Selatan", "Suhardin, S.Sos", "Jl. I Gusti Ngurah Rai, Tatura Sel., Kec. Palu Sel., Kota Palu, Sulawesi Tengah 94111"},
	{"Tatura Utara", "Palu Selatan", "Kantor Kelurahan Tatura Utara", "Firman H. Nipo, S.Pd., M.A.P", "Jl. Anoa, Tatura Utara, Kec. Palu Sel., Kota Palu, Sulawesi Tengah 94111"},
	{"Besusu Barat", "Palu Timur", "Kantor Kelurahan Besusu Barat", "Adriani, S.Sos", "Jl. DR. Suharso No.18, Besusu Bar., Kec. Palu Tim., Kota Palu, Sulawesi Tengah 94118"},
	{"Besusu Tengah", "Palu Timur", "Kantor Kelurahan Besusu Tengah", "Hardi Eka Yanto Tsar, S.Sos., M.A.P", "Jl. Gatot Subroto No.16, Besusu Tengah, Kec. Palu Tim., Kota Palu, Sulawesi Tengah 94118"},
	{"Besusu Timur", "Palu Timur", "Kantor Kelurahan Besusu Timur", "Deden A. Wijaya, SH", "Jl. Urip Sumoharjo No.6, Besusu Tim., Kec. Palu Tim., Kota Palu, Sulawesi Tengah 94118"},
	{"Lolu Selatan", "Palu Timur", "Kantor Kelurahan Lolu Selatan", "Fadli Sabola, S.Kom.", "Jl. Ongka Malino No.17, Lolu Sel., Kec. Palu Sel., Kota Palu, Sulawesi Tengah 94111"},
	{"Lolu Utara", "Palu Timur", "Kantor Kelurahan Lolu Utara", "Abdul Arifin, S.Sos", "Jl. Wolter Monginsidi, Lolu Utara, Kec. Palu Tim., Kota Palu, Sulawesi Tengah 94112"},
	{"Boyaoge", "Tatanga", "Kantor Kelurahan Boyaoge", "Idrus Mansur, S.E", "Jl. Anggur IV, Boyaoge, Kec. Tatanga, Kota Palu, Sulawesi Tengah 94222"},
	{"Duyu", "Tatanga", "Kantor Kelurahan Duyu", "Erni, S.A.P", "Jl. Padanjakaya Duyu, Tatanga, Palu City, Central Sulawesi"},
	{"Nunu", "Tatanga", "Kantor Kelurahan Nunu", "Astuti Wulandari, S.Sos", "Jl. Beringin lorong02 no 02, Nunu, Kec. Tatanga, Kota Palu, Sulawesi Tengah 94222"},
	{"Palupi", "Tatanga", "Kantor Kelurahan Palupi", "Sry Rahayu, S.H., M.H.", "Jl. Poros Palu Bangga Palupi, Ulujadi, Palu City, Central Sulawesi 94222"},
	{"Pengawu", "Tatanga", "Kantor Kelurahan Pengawu", "Boby Rosan Hedar, S.H", "Jl. Malontara No.12, Pengawu, Kec. Tatanga, Kota Palu, Sulawesi Tengah"},
	{"Tavanjuka", "Tatanga", "Kantor Kelurahan Tavanjuka", "Putra Maharandha Airlangga, S.STP., M.H", "Jl. Anggur IV, Boyaoge, Kec. Tatanga, Kota Palu, Sulawesi Tengah 94222"},
	{"Mamboro", "Palu Utara", "Kantor Kelurahan Mamboro", "Moh Gusfan, SH", "Jl. Trans Sulawesi, Kec. Palu Utara, Palu, Sulawesi Tengah 94145"},
	{"Mamboro Barat", "Palu Utara", "Kantor Kelurahan Mamboro Barat", "Megawaty Muid, SE", "Jl. Trans Sulawesi No.153, Mamboro, Kec. Palu Utara, Palu, Sulawesi Tengah 94145"},
}

var letterDistrictNames = []string{"Mantikulore", "Palu Barat", "Palu Selatan", "Palu Timur", "Palu Utara", "Tatanga"}

var districts = []string{"Mantikulore", "Palu Barat", "Palu Selatan", "Palu Timur", "Palu Utara", "Tatanga", "Tawaeli", "Ulujadi"}

var neighborhoods = map[string][]string{
	"Mantikulore":  {"Talise", "Talise Valangguni", "Tondo", "Layana Indah", "Tanamodindi"},
	"Palu Barat":   {"Balaroa", "Kamonji", "Lere", "Siranindi"},
	"Palu Selatan": {"Birobuli Selatan", "Birobuli Utara", "Petobo", "Tatura Selatan"},
	"Palu Timur":   {"Besusu Barat", "Besusu Tengah", "Besusu Timur", "Lasoani"},
	"Palu Utara":   {"Kayumalue Ngapa", "Mamboro", "Taipa"},
	"Tatanga":      {"Duyu", "Nunu", "Tatura Utara"},
	"Tawaeli":      {"Baiya", "Lasoani", "Panau"},
	"Ulujadi":      {"Buluri", "Donggala Kodi", "Silae"},
}

func letterNeighborhoods() map[string][]string {
	result := make(map[string][]string)
	for _, office := range letterOffices {
		result[office.District] = append(result[office.District], office.Neighborhood)
	}
	return result
}

func letterOffice(district, neighborhood string) (LetterOffice, bool) {
	for _, office := range letterOffices {
		if office.District == district && office.Neighborhood == neighborhood {
			return office, true
		}
	}
	return LetterOffice{}, false
}

func validLetterOffice(district, neighborhood string) bool {
	_, ok := letterOffice(district, neighborhood)
	return ok
}

func requestBelongsToOffice(user User, request SuratRequest) bool {
	if user.Role != "staf" && user.Role != "lurah" {
		return false
	}
	if _, ok := letterOffice(user.OfficeDistrict, user.OfficeNeighborhood); !ok {
		return false
	}
	return request.District == user.OfficeDistrict && request.Neighborhood == user.OfficeNeighborhood
}

func residentBelongsToOffice(staff, resident User) bool {
	if staff.Role != "staf" || resident.Role != "warga" {
		return false
	}
	if _, ok := letterOffice(staff.OfficeDistrict, staff.OfficeNeighborhood); !ok {
		return false
	}
	return resident.HomeDistrict == staff.OfficeDistrict &&
		resident.HomeNeighborhood == staff.OfficeNeighborhood
}

func canAccessLetter(user User, request SuratRequest) bool {
	if user.Role == "warga" {
		return request.UserID == user.ID
	}
	return requestBelongsToOffice(user, request)
}

func officeLetterEmail(office LetterOffice) string {
	if office.District == "Mantikulore" && office.Neighborhood == "Tondo" {
		return "tondo@palukota.go.id"
	}
	return ""
}

func officeLetterAddress(office LetterOffice) string {
	address := "Alamat: " + office.Address
	if email := officeLetterEmail(office); email != "" {
		address += " | Email: " + email
	}
	return address
}

func main() {
	dataPath := envOr("SILAYANG_DATA", filepath.Join("data", "silayang.json"))
	uploadDir := envOr("SILAYANG_UPLOADS", "uploads")
	defaultSecret := "silayang-dev-secret-change-this-before-deployment"
	secret := []byte(envOr("SILAYANG_SECRET", defaultSecret))
	if string(secret) == defaultSecret {
		log.Print("PERINGATAN: SILAYANG_SECRET masih memakai nilai development; ganti sebelum deployment.")
	}

	store, err := openStore(dataPath)
	if err != nil {
		log.Fatal(err)
	}
	if err := seedDemoUsers(store); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(uploadDir, 0700); err != nil {
		log.Fatal(err)
	}
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"roleName": func(role string) string {
			return map[string]string{"warga": "Warga", "staf": "Staf verifikator", "lurah": "Lurah"}[role]
		},
		"statusName": statusName,
		"statusClass": func(status string) string {
			switch status {
			case "selesai", "aktif", "draft_ready":
				return "success"
			case "ditolak", "ditolak_akun":
				return "danger"
			case "perlu_revisi", "pending", "pending_verifikasi", "menunggu":
				return "warning"
			default:
				return "info"
			}
		},
		"fieldKey":   fieldKey,
		"join":       strings.Join,
		"date":       func(t time.Time) string { return t.Format("02 Jan 2006, 15:04") },
		"lookupUser": func(id string) User { return store.snapshot().Users[id] },
	}).ParseGlob(filepath.Join("templates", "*.html"))
	if err != nil {
		log.Fatalf("memuat template: %v", err)
	}
	app := &App{
		store: store, templates: tmpl, secret: secret, uploadDir: uploadDir,
		secureCookie: strings.EqualFold(os.Getenv("SILAYANG_COOKIE_SECURE"), "true"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", app.home)
	mux.HandleFunc("GET /login", app.loginPage)
	mux.HandleFunc("POST /login", app.login)
	mux.HandleFunc("GET /register", app.registerPage)
	mux.HandleFunc("POST /register", app.register)
	mux.HandleFunc("POST /logout", app.logout)
	mux.HandleFunc("GET /dashboard", app.dashboard)
	mux.HandleFunc("GET /surat/baru", app.newSuratPage)
	mux.HandleFunc("POST /surat", app.createSurat)
	mux.HandleFunc("POST /api/v1/letters/draft-preview", app.draftPreview)
	mux.HandleFunc("GET /aduan/baru", app.newComplaintPage)
	mux.HandleFunc("POST /aduan", app.createComplaint)
	mux.HandleFunc("GET /tracking", app.tracking)
	mux.HandleFunc("GET /admin", app.adminPage)
	mux.HandleFunc("POST /admin/warga/{id}", app.reviewResident)
	mux.HandleFunc("POST /admin/surat/{id}", app.reviewSurat)
	mux.HandleFunc("POST /admin/aduan/{id}", app.reviewComplaint)
	mux.HandleFunc("GET /dokumen/{id}", app.document)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.HandleFunc("GET /uploads/{name}", app.upload)

	addr := envOr("SILAYANG_ADDR", ":8080")
	displayAddr := addr
	if strings.HasPrefix(addr, ":") {
		displayAddr = "localhost" + addr
	}
	log.Printf("SILAYANG berjalan di http://%s", displayAddr)
	log.Fatal(http.ListenAndServe(addr, logRequests(mux)))
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func seedDemoUsers(store *Store) error {
	snapshot := store.snapshot()
	if len(snapshot.Users) == 0 {
		demo := []User{
			{ID: "demo-warga", NIK: "7271010101900001", Name: "Ayu Pratiwi", Email: "warga@silayang.local", Phone: "081234567890", BirthPlace: "Palu", BirthDate: "1990-01-01", Gender: "P", Address: "Jl. Mawar No. 12, Kel. Talise, Palu", Occupation: "Wiraswasta", Role: "warga", AccountStatus: "aktif"},
			{ID: "demo-staf", NIK: "7271010101800002", Name: "Rizal Hidayat", Email: "staf@silayang.local", Phone: "081234567891", BirthDate: "1980-01-01", Gender: "L", Address: "Kantor Kelurahan", Role: "staf", AccountStatus: "aktif"},
			{ID: "demo-lurah", NIK: "7271010101700003", Name: "Nur Aisyah", Email: "lurah@silayang.local", Phone: "081234567892", BirthDate: "1970-01-01", Gender: "P", Address: "Kantor Kelurahan", Role: "lurah", AccountStatus: "aktif"},
		}
		for i := range demo {
			demo[i].CreatedAt = time.Now()
			demo[i].PasswordHash = hashPassword("silayang123")
		}
		if err := store.update(func(db *database) error {
			for _, user := range demo {
				db.Users[user.ID] = user
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return store.update(func(db *database) error {
		for _, id := range []string{"demo-staf", "demo-lurah"} {
			user, ok := db.Users[id]
			if ok && user.OfficeDistrict == "" && user.OfficeNeighborhood == "" {
				user.OfficeDistrict, user.OfficeNeighborhood = "Mantikulore", "Talise"
				db.Users[id] = user
			}
		}

		defaultPasswordHash := ""
		for _, office := range letterOffices {
			for _, role := range []string{"staf", "lurah"} {
				id := officeAccountID(role, office)
				email := officeAccountEmail(role, office)
				account, exists := db.Users[id]
				if exists {
					if account.Role != role {
						return fmt.Errorf("akun wilayah %s memiliki peran tidak sesuai", id)
					}
					account.OfficeDistrict, account.OfficeNeighborhood = office.District, office.Neighborhood
					if account.AccountStatus == "" {
						account.AccountStatus = "aktif"
					}
					if account.PasswordHash == "" {
						if defaultPasswordHash == "" {
							defaultPasswordHash = hashPassword("silayang123")
						}
						account.PasswordHash = defaultPasswordHash
					}
					db.Users[id] = account
					continue
				}
				for otherID, other := range db.Users {
					if strings.EqualFold(other.Email, email) {
						return fmt.Errorf("email akun wilayah %s sudah dipakai oleh akun %s", email, otherID)
					}
				}
				if defaultPasswordHash == "" {
					defaultPasswordHash = hashPassword("silayang123")
				}
				name := "Staf Kelurahan " + office.Neighborhood
				if role == "lurah" {
					name = office.Mayor
				}
				db.Users[id] = User{
					ID: id, Name: name, Email: email, Role: role, AccountStatus: "aktif",
					OfficeDistrict: office.District, OfficeNeighborhood: office.Neighborhood,
					PasswordHash: defaultPasswordHash, CreatedAt: time.Now(),
				}
			}
		}
		for id, resident := range db.Users {
			if resident.Role != "warga" || (resident.HomeDistrict != "" && resident.HomeNeighborhood != "") {
				continue
			}
			office, ok := inferLetterOfficeFromAddress(resident.Address)
			if !ok {
				continue
			}
			resident.HomeDistrict, resident.HomeNeighborhood = office.District, office.Neighborhood
			db.Users[id] = resident
		}
		for id, request := range db.Requests {
			if request.District != "" && request.Neighborhood != "" {
				continue
			}
			office, ok := inferLetterOfficeFromAddress(request.Applicant["alamat"])
			if !ok {
				continue
			}
			request.District, request.Neighborhood = office.District, office.Neighborhood
			db.Requests[id] = request
		}
		return nil
	})
}

func officeAccountID(role string, office LetterOffice) string {
	slug := strings.ReplaceAll(fieldKey(office.District+" "+office.Neighborhood), "_", "-")
	return role + "-" + slug
}

func officeAccountEmail(role string, office LetterOffice) string {
	slug := strings.ReplaceAll(fieldKey(office.District+" "+office.Neighborhood), "_", "-")
	return role + "." + slug + "@silayang.local"
}

func inferLetterOfficeFromAddress(address string) (LetterOffice, bool) {
	normalized := strings.ToLower(strings.NewReplacer(".", " ", ",", " ", ";", " ", ":", " ").Replace(address))
	tokens := strings.Fields(normalized)
	for i, token := range tokens {
		if token != "kel" && token != "kelurahan" {
			continue
		}
		remaining := tokens[i+1:]
		bestLength := 0
		var match LetterOffice
		ambiguous := false
		for _, office := range letterOffices {
			name := strings.Fields(strings.ToLower(office.Neighborhood))
			if len(name) <= bestLength || len(name) > len(remaining) {
				continue
			}
			matched := true
			for j := range name {
				if remaining[j] != name[j] {
					matched = false
					break
				}
			}
			if matched {
				switch {
				case len(name) > bestLength:
					bestLength, match, ambiguous = len(name), office, false
				case len(name) == bestLength && (match.District != office.District || match.Neighborhood != office.Neighborhood):
					ambiguous = true
				}
			}
		}
		if bestLength > 0 && !ambiguous {
			return match, true
		}
	}
	return LetterOffice{}, false
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	user := a.currentUser(r)
	if user != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	a.render(w, http.StatusOK, "home", PageData{Title: "Layanan Warga Digital", Services: services, Categories: complaintCategories})
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, http.StatusOK, "auth", PageData{Title: "Masuk", Code: "login"})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderError(w, "login", "Formulir tidak dapat dibaca.")
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	pass := r.FormValue("password")
	var found *User
	for _, candidate := range a.store.snapshot().Users {
		if strings.EqualFold(candidate.Email, email) {
			copyUser := candidate
			found = &copyUser
			break
		}
	}
	if found == nil || !verifyPassword(found.PasswordHash, pass) {
		a.renderError(w, "login", "Email atau kata sandi tidak sesuai.")
		return
	}
	if err := a.setSession(w, found.ID); err != nil {
		http.Error(w, "Gagal membuat sesi masuk.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (a *App) registerPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, http.StatusOK, "auth", PageData{
		Title: "Daftar akun warga", Code: "register",
		Districts: letterDistrictNames, Neighborhoods: letterNeighborhoods(),
	})
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		a.renderError(w, "register", "Data pendaftaran tidak valid atau terlalu besar.")
		return
	}
	user := User{
		ID: randomID(), NIK: strings.TrimSpace(r.FormValue("nik")), Name: strings.TrimSpace(r.FormValue("name")),
		Email: strings.ToLower(strings.TrimSpace(r.FormValue("email"))), Phone: strings.TrimSpace(r.FormValue("phone")),
		BirthPlace: strings.TrimSpace(r.FormValue("birth_place")), BirthDate: r.FormValue("birth_date"),
		Gender: r.FormValue("gender"), Address: strings.TrimSpace(r.FormValue("address")),
		Occupation:       strings.TrimSpace(r.FormValue("occupation")),
		HomeDistrict:     strings.TrimSpace(r.FormValue("home_district")),
		HomeNeighborhood: strings.TrimSpace(r.FormValue("home_neighborhood")),
		Role:             "warga", AccountStatus: "pending", PasswordHash: hashPassword(r.FormValue("password")), CreatedAt: time.Now(),
	}
	password := r.FormValue("password")
	if len(user.NIK) != 16 || !digitsOnly(user.NIK) || user.Name == "" || user.Email == "" || user.Phone == "" ||
		user.BirthDate == "" || (user.Gender != "L" && user.Gender != "P") || user.Address == "" ||
		!validLetterOffice(user.HomeDistrict, user.HomeNeighborhood) || len(password) < 8 {
		a.renderError(w, "register", "Lengkapi semua data. NIK harus 16 digit dan kata sandi minimal 8 karakter.")
		return
	}
	for _, existing := range a.store.snapshot().Users {
		if existing.NIK == user.NIK || strings.EqualFold(existing.Email, user.Email) {
			a.renderError(w, "register", "NIK atau email sudah digunakan.")
			return
		}
	}
	path, err := a.saveUpload(r, "ktp", "image/jpeg", "image/png")
	if err != nil && !errors.Is(err, http.ErrMissingFile) {
		a.renderError(w, "register", err.Error())
		return
	}
	user.KTPPath = path
	if err := a.store.update(func(db *database) error {
		for _, existing := range db.Users {
			if existing.NIK == user.NIK || strings.EqualFold(existing.Email, user.Email) {
				return errors.New("NIK atau email sudah digunakan")
			}
		}
		db.Users[user.ID] = user
		return nil
	}); err != nil {
		a.renderError(w, "register", "Pendaftaran gagal disimpan: "+err.Error())
		return
	}
	if err := a.setSession(w, user.ID); err != nil {
		http.Error(w, "Pendaftaran tersimpan, tetapi sesi tidak dapat dibuat.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard?notice=pending", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	user := a.requireUser(w, r)
	if user == nil {
		return
	}
	data := PageData{
		Title: "Ringkasan", User: user, Services: services, Categories: complaintCategories,
		Notice: dashboardNotice(*user, r.URL.Query().Get("notice")),
	}
	snapshot := a.store.snapshot()
	for _, item := range snapshot.Requests {
		if canAccessLetter(*user, item) {
			data.Requests = append(data.Requests, item)
		}
	}
	for _, item := range snapshot.Complaints {
		if item.UserID == user.ID || user.Role != "warga" {
			data.Complaints = append(data.Complaints, item)
		}
	}
	sort.Slice(data.Requests, func(i, j int) bool { return data.Requests[i].CreatedAt.After(data.Requests[j].CreatedAt) })
	sort.Slice(data.Complaints, func(i, j int) bool { return data.Complaints[i].CreatedAt.After(data.Complaints[j].CreatedAt) })
	for _, resident := range snapshot.Users {
		if resident.AccountStatus == "pending" && residentBelongsToOffice(*user, resident) {
			data.Stats.Residents++
		}
	}
	for _, request := range snapshot.Requests {
		if !requestBelongsToOffice(*user, request) {
			continue
		}
		switch request.Status {
		case "pending_verifikasi":
			data.Stats.Pending++
		case "draft_ready":
			data.Stats.Processing++
		case "selesai":
			data.Stats.Completed++
		}
	}
	data.Stats.Complaints = len(snapshot.Complaints)
	a.render(w, http.StatusOK, "dashboard", data)
}

func (a *App) newSuratPage(w http.ResponseWriter, r *http.Request) {
	user := a.requireActiveResident(w, r)
	if user == nil {
		return
	}
	a.render(w, http.StatusOK, "surat_form", PageData{Title: "Ajukan surat", User: user, Services: services, Districts: letterDistrictNames, Neighborhoods: letterNeighborhoods()})
}

func (a *App) createSurat(w http.ResponseWriter, r *http.Request) {
	user := a.requireActiveResident(w, r)
	if user == nil {
		return
	}
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		a.renderFormError(w, "surat_form", "Ukuran berkas melebihi batas 5 MB atau formulir tidak valid.", user)
		return
	}
	service := serviceByCode(r.FormValue("service_code"))
	if service == nil {
		a.renderFormError(w, "surat_form", "Pilih jenis layanan surat yang tersedia.", user)
		return
	}
	district := strings.TrimSpace(r.FormValue("kecamatan"))
	neighborhood := strings.TrimSpace(r.FormValue("kelurahan"))
	if _, ok := letterOffice(district, neighborhood); !ok {
		a.renderFormError(w, "surat_form", "Pilih kecamatan dan kelurahan yang valid sebelum memilih layanan surat.", user)
		return
	}
	purpose := strings.TrimSpace(r.FormValue("keperluan"))
	if purpose == "" {
		a.renderFormError(w, "surat_form", "Tujuan atau keperluan surat wajib diisi.", user)
		return
	}
	rtNumber := strings.TrimSpace(r.FormValue("rt_letter_number"))
	if rtNumber == "" {
		a.renderFormError(w, "surat_form", "Nomor surat pengantar RT wajib diisi.", user)
		return
	}
	applicant := make(map[string]string)
	for _, key := range []string{"nama", "nik", "tempat_lahir", "tanggal_lahir", "jenis_kelamin", "agama", "kewarganegaraan", "pekerjaan", "alamat"} {
		value := strings.TrimSpace(r.FormValue("applicant_" + key))
		if value != "" {
			applicant[key] = value
		}
	}
	if r.FormValue("is_self") == "true" {
		applicant["nama"], applicant["nik"], applicant["tempat_lahir"], applicant["tanggal_lahir"], applicant["jenis_kelamin"], applicant["pekerjaan"], applicant["alamat"] =
			user.Name, user.NIK, user.BirthPlace, user.BirthDate, user.Gender, user.Occupation, user.Address
		if applicant["pekerjaan"] == "" {
			applicant["pekerjaan"] = strings.TrimSpace(r.FormValue("applicant_pekerjaan"))
		}
	}
	if applicant["nama"] == "" || len(applicant["nik"]) != 16 || !digitsOnly(applicant["nik"]) {
		a.renderFormError(w, "surat_form", "Nama dan NIK subjek surat wajib diisi; NIK harus 16 digit.", user)
		return
	}
	specific := map[string]string{}
	for _, field := range service.Fields {
		key := fieldKey(field)
		if value := strings.TrimSpace(r.FormValue("specific_" + key)); value != "" {
			specific[field] = value
		}
	}
	attachments := []string{}
	for _, field := range []string{"rt_attachment", "extra_attachment"} {
		path, err := a.saveUpload(r, field, "image/jpeg", "image/png", "application/pdf")
		if err != nil && !errors.Is(err, http.ErrMissingFile) {
			a.renderFormError(w, "surat_form", err.Error(), user)
			return
		}
		if path != "" {
			attachments = append(attachments, path)
		}
	}
	now := time.Now()
	request := SuratRequest{
		ID: randomID(), TrackingCode: trackingCode("REQ"), UserID: user.ID, ServiceCode: service.Code,
		ServiceName: service.Name, District: district, Neighborhood: neighborhood,
		IsSelf: r.FormValue("is_self") == "true", Applicant: applicant, Specific: specific, Purpose: purpose,
		RTLetterNumber: rtNumber, Attachments: attachments,
		Status: "pending_verifikasi", CreatedAt: now, UpdatedAt: now,
	}
	if err := a.store.update(func(db *database) error {
		db.Requests[request.ID] = request
		return nil
	}); err != nil {
		a.renderFormError(w, "surat_form", "Permohonan gagal disimpan: "+err.Error(), user)
		return
	}
	http.Redirect(w, r, "/dashboard?notice=request-created", http.StatusSeeOther)
}

func (a *App) draftPreview(w http.ResponseWriter, r *http.Request) {
	writeJSON := func(status int, response DraftPreviewResponse) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("menulis respons pratinjau draf: %v", err)
		}
	}
	user := a.currentUser(r)
	if user == nil {
		writeJSON(http.StatusUnauthorized, DraftPreviewResponse{Status: "error", Message: "Silakan masuk untuk membuat pratinjau surat."})
		return
	}
	if user.Role != "warga" || user.AccountStatus != "aktif" {
		writeJSON(http.StatusForbidden, DraftPreviewResponse{Status: "error", Message: "Hanya akun warga terverifikasi yang dapat membuat pratinjau surat."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var input DraftPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(http.StatusBadRequest, DraftPreviewResponse{Status: "error", Message: "Data pratinjau tidak valid."})
		return
	}
	if input.UserID == "" || input.UserID != user.ID {
		writeJSON(http.StatusForbidden, DraftPreviewResponse{Status: "error", Message: "ID warga tidak sesuai dengan akun yang sedang masuk."})
		return
	}
	service := serviceByCode(input.ServiceCode)
	if service == nil {
		writeJSON(http.StatusBadRequest, DraftPreviewResponse{Status: "error", Message: "Jenis surat tidak tersedia."})
		return
	}
	district := strings.TrimSpace(input.Additional["kecamatan"])
	neighborhood := strings.TrimSpace(input.Additional["kelurahan"])
	office, ok := letterOffice(district, neighborhood)
	if !ok {
		writeJSON(http.StatusBadRequest, DraftPreviewResponse{Status: "error", Message: "Pilih kecamatan dan kelurahan yang valid."})
		return
	}
	purpose := strings.TrimSpace(input.Additional["keperluan"])
	if purpose == "" {
		writeJSON(http.StatusUnprocessableEntity, DraftPreviewResponse{Status: "error", Message: "Tujuan atau keperluan surat wajib diisi."})
		return
	}

	isSelf := input.Additional["is_self"] != "false"
	name, nik := user.Name, user.NIK
	birthPlace, birthDate, gender := user.BirthPlace, user.BirthDate, user.Gender
	address, occupation := user.Address, user.Occupation
	if !isSelf {
		name = strings.TrimSpace(input.Additional["pemohon_nama"])
		nik = strings.TrimSpace(input.Additional["pemohon_nik"])
		birthPlace = strings.TrimSpace(input.Additional["pemohon_tempat_lahir"])
		birthDate = strings.TrimSpace(input.Additional["pemohon_tanggal_lahir"])
		gender = strings.TrimSpace(input.Additional["pemohon_jenis_kelamin"])
		address = strings.TrimSpace(input.Additional["pemohon_alamat"])
		occupation = strings.TrimSpace(input.Additional["pemohon_pekerjaan"])
	} else if occupation == "" {
		occupation = strings.TrimSpace(input.Additional["pekerjaan"])
	}
	if name == "" || len(nik) != 16 || !digitsOnly(nik) {
		writeJSON(http.StatusUnprocessableEntity, DraftPreviewResponse{Status: "error", Message: "Nama pemohon dan NIK 16 digit wajib tersedia."})
		return
	}
	if occupation == "" {
		writeJSON(http.StatusUnprocessableEntity, DraftPreviewResponse{Status: "error", Message: "Pekerjaan belum diisi. Masukkan pekerjaan untuk melanjutkan pratinjau."})
		return
	}

	genderName := map[string]string{"L": "Laki-laki", "P": "Perempuan"}[gender]
	if genderName == "" {
		genderName = gender
	}
	birth := formatBirth(birthPlace, birthDate)
	var additionalFields []DraftField
	for _, field := range service.Fields {
		if value := strings.TrimSpace(input.Additional["specific_"+fieldKey(field)]); value != "" {
			additionalFields = append(additionalFields, DraftField{Label: field, Value: value})
		}
	}
	now := time.Now()
	draft := DraftData{
		LetterName: service.Name,
		Header:     fmt.Sprintf("PEMERINTAH KOTA PALU\nKECAMATAN %s\nKELURAHAN %s\n%s", strings.ToUpper(district), strings.ToUpper(neighborhood), officeLetterAddress(office)),
		District:   district, Neighborhood: neighborhood, OfficeName: office.Name, OfficeAddress: office.Address, OfficeEmail: officeLetterEmail(office),
		FullName: name, NIK: nik, Birth: birth,
		Gender: genderName, Occupation: occupation, Address: address, Purpose: purpose, OfficialNumber: "",
		DraftDate: formatDateID(now), MayorName: office.Mayor, MayorNIP: "000000000000000000 (DUMMY)",
		AdditionalFields: additionalFields,
	}
	writeJSON(http.StatusOK, DraftPreviewResponse{Status: "success", DraftData: &draft})
}

func (a *App) newComplaintPage(w http.ResponseWriter, r *http.Request) {
	user := a.requireActiveResident(w, r)
	if user == nil {
		return
	}
	a.render(w, http.StatusOK, "complaint_form", PageData{Title: "Sampaikan aduan", User: user, Categories: complaintCategories})
}

func (a *App) createComplaint(w http.ResponseWriter, r *http.Request) {
	user := a.requireActiveResident(w, r)
	if user == nil {
		return
	}
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		a.renderFormError(w, "complaint_form", "Ukuran lampiran melebihi batas 5 MB atau formulir tidak valid.", user)
		return
	}
	category := strings.TrimSpace(r.FormValue("category"))
	district := strings.TrimSpace(r.FormValue("district"))
	neighborhood := strings.TrimSpace(r.FormValue("neighborhood"))
	description := strings.TrimSpace(r.FormValue("description"))
	if !contains(complaintCategories, category) || !contains(districts, district) ||
		!contains(neighborhoods[district], neighborhood) || len(description) < 15 {
		a.renderFormError(w, "complaint_form", "Pilih kategori dan lokasi yang valid. Deskripsi minimal 15 karakter.", user)
		return
	}
	evidence, err := a.saveUpload(r, "evidence", "image/jpeg", "image/png", "application/pdf")
	if err != nil && !errors.Is(err, http.ErrMissingFile) {
		a.renderFormError(w, "complaint_form", err.Error(), user)
		return
	}
	now := time.Now()
	complaint := Complaint{
		ID: randomID(), TrackingCode: trackingCode("ADV"), UserID: user.ID, Category: category,
		District: district, Neighborhood: neighborhood, Description: description, EvidencePath: evidence,
		Status: "menunggu", CreatedAt: now, UpdatedAt: now,
	}
	if err := a.store.update(func(db *database) error {
		db.Complaints[complaint.ID] = complaint
		return nil
	}); err != nil {
		a.renderFormError(w, "complaint_form", "Aduan gagal disimpan: "+err.Error(), user)
		return
	}
	http.Redirect(w, r, "/dashboard?notice=complaint-created", http.StatusSeeOther)
}

func (a *App) tracking(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	data := PageData{Title: "Lacak permohonan", Code: code}
	if code != "" {
		snapshot := a.store.snapshot()
		for _, request := range snapshot.Requests {
			if strings.EqualFold(request.TrackingCode, code) {
				copyRequest := request
				data.Request = &copyRequest
				break
			}
		}
		if data.Request == nil {
			for _, complaint := range snapshot.Complaints {
				if strings.EqualFold(complaint.TrackingCode, code) {
					copyComplaint := complaint
					data.Complaint = &copyComplaint
					break
				}
			}
		}
	}
	a.render(w, http.StatusOK, "tracking", data)
}

func (a *App) adminPage(w http.ResponseWriter, r *http.Request) {
	user := a.requireRole(w, r, "staf", "lurah")
	if user == nil {
		return
	}
	data := PageData{Title: "Panel administrasi", User: user, Notice: noticeFor(r.URL.Query().Get("notice"))}
	snapshot := a.store.snapshot()
	for _, resident := range snapshot.Users {
		if resident.AccountStatus == "pending" && residentBelongsToOffice(*user, resident) {
			data.Residents = append(data.Residents, resident)
		}
	}
	for _, request := range snapshot.Requests {
		if requestBelongsToOffice(*user, request) {
			data.Requests = append(data.Requests, request)
		}
	}
	for _, complaint := range snapshot.Complaints {
		data.Complaints = append(data.Complaints, complaint)
	}
	sort.Slice(data.Residents, func(i, j int) bool { return data.Residents[i].CreatedAt.Before(data.Residents[j].CreatedAt) })
	sort.Slice(data.Requests, func(i, j int) bool { return data.Requests[i].CreatedAt.Before(data.Requests[j].CreatedAt) })
	sort.Slice(data.Complaints, func(i, j int) bool { return data.Complaints[i].CreatedAt.Before(data.Complaints[j].CreatedAt) })
	a.render(w, http.StatusOK, "admin", data)
}

func (a *App) reviewResident(w http.ResponseWriter, r *http.Request) {
	user := a.requireRole(w, r, "staf")
	if user == nil {
		return
	}
	id := r.PathValue("id")
	action := r.FormValue("action")
	reason := strings.TrimSpace(r.FormValue("reason"))
	err := a.store.update(func(db *database) error {
		resident, ok := db.Users[id]
		if !ok || resident.AccountStatus != "pending" || !residentBelongsToOffice(*user, resident) {
			return errors.New("akun warga tidak ditemukan")
		}
		switch action {
		case "approve":
			resident.AccountStatus, resident.RejectionReason = "aktif", ""
		case "reject":
			if reason == "" {
				return errors.New("alasan penolakan wajib diisi")
			}
			resident.AccountStatus, resident.RejectionReason = "ditolak", reason
		default:
			return errors.New("aksi verifikasi tidak dikenal")
		}
		db.Users[id] = resident
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin?notice=resident-reviewed", http.StatusSeeOther)
}

func (a *App) reviewSurat(w http.ResponseWriter, r *http.Request) {
	user := a.requireRole(w, r, "staf", "lurah")
	if user == nil {
		return
	}
	id, action := r.PathValue("id"), r.FormValue("action")
	officialNumber := strings.TrimSpace(r.FormValue("official_number"))
	note := strings.TrimSpace(r.FormValue("note"))
	err := a.store.update(func(db *database) error {
		request, ok := db.Requests[id]
		if !ok || !requestBelongsToOffice(*user, request) {
			return errors.New("permohonan surat tidak ditemukan")
		}
		switch action {
		case "revision", "reject", "draft":
			if user.Role != "staf" {
				return errors.New("hanya staf verifikator yang dapat melakukan tindakan ini")
			}
			if (action == "revision" || action == "reject") && note == "" {
				return errors.New("catatan untuk warga wajib diisi")
			}
			if action == "revision" {
				request.Status = "perlu_revisi"
			} else if action == "reject" {
				request.Status = "ditolak"
			} else {
				if officialNumber == "" {
					return errors.New("nomor surat resmi wajib diisi sebelum membuat draft")
				}
				request.Status, request.OfficialNumber = "draft_ready", officialNumber
			}
			request.AdminNote = note
		case "approve":
			if user.Role != "lurah" {
				return errors.New("persetujuan dan TTE hanya dapat dilakukan oleh lurah")
			}
			if request.Status != "draft_ready" {
				return errors.New("permohonan harus berstatus draft siap sebelum disetujui")
			}
			request.Status = "selesai"
			request.AdminNote = "Disetujui oleh lurah. Tanda tangan, QR, dan NIP pada dokumen hanya tampilan simulasi."
		default:
			return errors.New("aksi permohonan tidak dikenal")
		}
		request.UpdatedAt = time.Now()
		db.Requests[id] = request
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin?notice=request-reviewed", http.StatusSeeOther)
}

func (a *App) reviewComplaint(w http.ResponseWriter, r *http.Request) {
	if a.requireRole(w, r, "staf", "lurah") == nil {
		return
	}
	id, action, reply := r.PathValue("id"), r.FormValue("action"), strings.TrimSpace(r.FormValue("reply"))
	if reply == "" {
		http.Error(w, "Tanggapan admin wajib diisi.", http.StatusBadRequest)
		return
	}
	err := a.store.update(func(db *database) error {
		complaint, ok := db.Complaints[id]
		if !ok {
			return errors.New("aduan tidak ditemukan")
		}
		if action != "diproses" && action != "selesai" && action != "ditolak" {
			return errors.New("status aduan tidak dikenal")
		}
		complaint.Status, complaint.AdminReply, complaint.UpdatedAt = action, reply, time.Now()
		db.Complaints[id] = complaint
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin?notice=complaint-reviewed", http.StatusSeeOther)
}

func (a *App) document(w http.ResponseWriter, r *http.Request) {
	user := a.requireUser(w, r)
	if user == nil {
		return
	}
	request, ok := a.store.snapshot().Requests[r.PathValue("id")]
	if !ok || !canAccessLetter(*user, request) ||
		(request.Status != "selesai" && request.Status != "draft_ready") ||
		(request.Status == "draft_ready" && user.Role == "warga") {
		http.NotFound(w, r)
		return
	}
	var output bytes.Buffer
	office, _ := letterOffice(request.District, request.Neighborhood)
	if err := a.templates.ExecuteTemplate(&output, "document", struct {
		Request     SuratRequest
		IsDraft     bool
		VerifyURL   string
		Office      LetterOffice
		OfficeEmail string
	}{request, request.Status == "draft_ready", "/tracking?code=" + request.TrackingCode, office, officeLetterEmail(office)}); err != nil {
		log.Printf("render dokumen: %v", err)
		http.Error(w, "Dokumen tidak dapat ditampilkan.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", request.TrackingCode+".html"))
	_, _ = w.Write(output.Bytes())
}

func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	user := a.requireUser(w, r)
	if user == nil {
		return
	}
	name := r.PathValue("name")
	if name == "" || filepath.Base(name) != name {
		http.NotFound(w, r)
		return
	}
	targetPath := "/uploads/" + name
	snapshot := a.store.snapshot()
	allowed := false
	for _, owner := range snapshot.Users {
		if owner.KTPPath == targetPath && residentBelongsToOffice(*user, owner) {
			allowed = true
			break
		}
	}
	if !allowed {
		for _, request := range snapshot.Requests {
			if contains(request.Attachments, targetPath) && canAccessLetter(*user, request) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		for _, complaint := range snapshot.Complaints {
			if complaint.EvidencePath == targetPath && (complaint.UserID == user.ID || user.Role == "staf" || user.Role == "lurah") {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		http.Error(w, "Anda tidak memiliki akses ke berkas ini.", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, filepath.Join(a.uploadDir, name))
}

func (a *App) render(w http.ResponseWriter, status int, page string, data PageData) {
	data.Page = page
	var output bytes.Buffer
	if err := a.templates.ExecuteTemplate(&output, page, data); err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "Halaman tidak dapat ditampilkan.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(status)
	_, _ = w.Write(output.Bytes())
}

func (a *App) renderError(w http.ResponseWriter, page, message string) {
	title := "Terjadi kesalahan"
	if page == "login" {
		title = "Masuk"
	} else if page == "register" {
		title = "Daftar akun warga"
	}
	data := PageData{Title: title, Code: page, Error: message}
	if page == "register" {
		data.Districts = letterDistrictNames
		data.Neighborhoods = letterNeighborhoods()
	}
	a.render(w, http.StatusBadRequest, "auth", data)
}

func (a *App) renderFormError(w http.ResponseWriter, page, message string, user *User) {
	title := "Ajukan surat"
	data := PageData{Title: title, User: user, Error: message}
	if page == "surat_form" {
		data.Services = services
		data.Districts = letterDistrictNames
		data.Neighborhoods = letterNeighborhoods()
	} else {
		data.Title = "Sampaikan aduan"
		data.Categories = complaintCategories
	}
	a.render(w, http.StatusBadRequest, page, data)
}

func (a *App) currentUser(r *http.Request) *User {
	cookie, err := r.Cookie(sessionName)
	if err != nil {
		return nil
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	mac := hmac.New(sha256.New, a.secret)
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil
	}
	values := strings.Split(string(payload), "|")
	if len(values) != 2 {
		return nil
	}
	expires, err := strconv.ParseInt(values[1], 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return nil
	}
	user, ok := a.store.snapshot().Users[values[0]]
	if !ok {
		return nil
	}
	return &user
}

func (a *App) setSession(w http.ResponseWriter, userID string) error {
	payload := []byte(fmt.Sprintf("%s|%d", userID, time.Now().Add(24*time.Hour).Unix()))
	mac := hmac.New(sha256.New, a.secret)
	mac.Write(payload)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name: sessionName, Value: value, Path: "/", HttpOnly: true, Secure: a.secureCookie,
		SameSite: http.SameSiteLaxMode, MaxAge: 86400,
	})
	return nil
}

func (a *App) requireUser(w http.ResponseWriter, r *http.Request) *User {
	user := a.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}
	return user
}

func (a *App) requireActiveResident(w http.ResponseWriter, r *http.Request) *User {
	user := a.requireUser(w, r)
	if user == nil {
		return nil
	}
	if user.Role != "warga" || user.AccountStatus != "aktif" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return nil
	}
	return user
}

func (a *App) requireRole(w http.ResponseWriter, r *http.Request, roles ...string) *User {
	user := a.requireUser(w, r)
	if user == nil {
		return nil
	}
	if !contains(roles, user.Role) {
		http.Error(w, "Anda tidak memiliki akses ke halaman ini.", http.StatusForbidden)
		return nil
	}
	return user
}

func (a *App) saveUpload(r *http.Request, field string, allowedTypes ...string) (string, error) {
	file, header, err := r.FormFile(field)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if header.Size > maxUploadSize {
		return "", errors.New("ukuran setiap berkas maksimal 5 MB")
	}
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("membaca unggahan: %w", err)
	}
	contentType := http.DetectContentType(buffer[:n])
	ok := false
	for _, allowed := range allowedTypes {
		if contentType == allowed {
			ok = true
			break
		}
	}
	if !ok {
		return "", errors.New("format berkas tidak didukung. Gunakan JPG, PNG, atau PDF sesuai kolom.")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("membaca ulang unggahan: %w", err)
	}
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "application/pdf": ".pdf"}[contentType]
	name := randomID() + ext
	target, err := os.OpenFile(filepath.Join(a.uploadDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("menyimpan unggahan: %w", err)
	}
	written, copyErr := io.Copy(target, io.LimitReader(file, maxUploadSize+1))
	closeErr := target.Close()
	if copyErr != nil {
		return "", fmt.Errorf("menyimpan unggahan: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("menutup berkas unggahan: %w", closeErr)
	}
	if written > maxUploadSize {
		os.Remove(filepath.Join(a.uploadDir, name))
		return "", errors.New("ukuran setiap berkas maksimal 5 MB")
	}
	return "/uploads/" + name, nil
}

func serviceByCode(code string) *Service {
	for i := range services {
		if services[i].Code == code {
			return &services[i]
		}
	}
	return nil
}

func fieldKey(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '-':
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func trackingCode(prefix string) string {
	return fmt.Sprintf("%s-%s-%s", prefix, time.Now().Format("20060102"), strings.ToUpper(randomID()[:6]))
}

func formatDateID(value time.Time) string {
	months := [...]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}
	return fmt.Sprintf("%02d %s %04d", value.Day(), months[int(value.Month())-1], value.Year())
}

func formatBirth(place, date string) string {
	formattedDate := strings.TrimSpace(date)
	if parsed, err := time.Parse("2006-01-02", formattedDate); err == nil {
		formattedDate = formatDateID(parsed)
	}
	if formattedDate == "" {
		formattedDate = "Belum diisi"
	}
	place = strings.TrimSpace(place)
	if place == "" {
		return formattedDate
	}
	return place + ", " + formattedDate
}

func randomID() string {
	value, err := randomHex(16)
	if err != nil {
		panic(fmt.Sprintf("crypto/rand gagal: %v", err))
	}
	return value
}

func randomHex(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func digitsOnly(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func noticeFor(code string) string {
	notices := map[string]string{
		"pending":            "Pendaftaran berhasil. Akun Anda menunggu verifikasi staf kelurahan.",
		"request-created":    "Permohonan surat berhasil dikirim. Simpan kode tracking untuk memantau proses.",
		"complaint-created":  "Aduan berhasil dikirim. Simpan kode tracking untuk melihat tindak lanjut.",
		"resident-reviewed":  "Verifikasi akun warga berhasil diperbarui.",
		"request-reviewed":   "Status permohonan surat berhasil diperbarui.",
		"complaint-reviewed": "Tindak lanjut aduan berhasil diperbarui.",
	}
	return notices[code]
}

func dashboardNotice(user User, code string) string {
	if code != "pending" || user.Role != "warga" {
		return noticeFor(code)
	}
	switch user.AccountStatus {
	case "pending":
		return noticeFor(code)
	case "aktif":
		return "Akun Anda sudah diverifikasi. Silakan lanjutkan menggunakan layanan SILAYANG."
	default:
		return ""
	}
}

func statusName(status string) string {
	statuses := map[string]string{
		"pending":            "Menunggu verifikasi",
		"aktif":              "Aktif",
		"ditolak":            "Ditolak",
		"pending_verifikasi": "Menunggu verifikasi",
		"perlu_revisi":       "Perlu perbaikan",
		"draft_ready":        "Draft siap · menunggu lurah",
		"selesai":            "Selesai",
		"menunggu":           "Menunggu tanggapan",
		"diproses":           "Sedang diproses",
	}
	if label, ok := statuses[status]; ok {
		return label
	}
	return status
}

func hashPassword(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(fmt.Sprintf("crypto/rand gagal: %v", err))
	}
	const iterations = 120000
	hash := pbkdf2SHA256([]byte(password), salt, iterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 10000 || iterations > 1000000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) != 32 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iterations, len(want))
	return hmac.Equal(got, want)
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLength int) []byte {
	const hashLength = 32
	blocks := (keyLength + hashLength - 1) / hashLength
	result := make([]byte, 0, blocks*hashLength)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		result = append(result, t...)
	}
	return result[:keyLength]
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/static/app.css" && r.URL.Path != "/static/app.js" {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}
