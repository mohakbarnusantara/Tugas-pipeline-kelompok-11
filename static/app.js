document.addEventListener("DOMContentLoaded", () => {
  const toggle = document.querySelector(".menu-toggle");
  const nav = document.querySelector(".main-nav");
  if (toggle && nav) {
    toggle.addEventListener("click", () => {
      const open = nav.classList.toggle("nav-open");
      toggle.setAttribute("aria-expanded", String(open));
    });
  }

  const selfToggle = document.getElementById("is-self");
  if (selfToggle) {
    const fields = {
      nama: { element: document.querySelector('[name="applicant_nama"]'), profile: "profileName" },
      nik: { element: document.querySelector('[name="applicant_nik"]'), profile: "profileNik" },
      tempat_lahir: { element: document.querySelector('[name="applicant_tempat_lahir"]'), profile: "profileBirthPlace" },
      tanggal_lahir: { element: document.querySelector('[name="applicant_tanggal_lahir"]'), profile: "profileBirth" },
      jenis_kelamin: { element: document.querySelector('[name="applicant_jenis_kelamin"]'), profile: "profileGender" },
      pekerjaan: { element: document.querySelector('[name="applicant_pekerjaan"]'), profile: "profileOccupation" },
      alamat: { element: document.querySelector('[name="applicant_alamat"]'), profile: "profileAddress" },
    };
    const fillFromProfile = (enabled) => {
      for (const field of Object.values(fields)) {
        const input = field.element;
        if (!input) continue;
        input.value = enabled ? input.dataset[field.profile] || "" : "";
      }
    };
    selfToggle.addEventListener("change", () => fillFromProfile(selfToggle.checked));
  }

  const serviceSelect = document.getElementById("service-select");
  const fieldContainer = document.getElementById("specific-fields");
  const hint = document.getElementById("service-hint");
  const extraLabel = document.getElementById("extra-doc-label");
  if (serviceSelect && fieldContainer) {
    serviceSelect.addEventListener("change", () => {
      const option = serviceSelect.selectedOptions[0];
      fieldContainer.replaceChildren();
      if (!option || !option.value) {
        hint.textContent = "Setiap layanan dapat meminta data khusus dan berkas pendukung.";
        extraLabel?.classList.remove("required-doc");
        return;
      }
      const labels = (option.dataset.fields || "").split("|").filter(Boolean);
      hint.textContent = labels.length
        ? `Data tambahan untuk layanan ini: ${labels.join(", ")}.`
        : "Tidak ada data khusus tambahan untuk layanan ini.";
      labels.forEach((label) => {
        const wrap = document.createElement("label");
        wrap.textContent = label;
        const input = document.createElement(label.toLowerCase().includes("tanggal") ? "input" : "textarea");
        const key = label.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "");
        input.name = `specific_${key}`;
        input.placeholder = `Masukkan ${label.toLowerCase()}`;
        if (input.tagName === "INPUT") input.type = "date";
        else input.rows = 2;
        wrap.append(input);
        fieldContainer.append(wrap);
      });
      if (option.dataset.extra) {
        const label = extraLabel?.querySelector("input");
        if (label) label.setAttribute("aria-label", option.dataset.extra);
        const caption = extraLabel?.querySelector(".label-help");
        if (caption) caption.textContent = `(${option.dataset.extra} · JPG, PNG, atau PDF · maks. 5 MB)`;
        extraLabel?.classList.add("required-doc");
      } else {
        extraLabel?.classList.remove("required-doc");
        const caption = extraLabel?.querySelector(".label-help");
        if (caption) caption.textContent = "(jika dipersyaratkan · maks. 5 MB)";
      }
    });
  }

  const districts = {
    Mantikulore: ["Talise", "Talise Valangguni", "Tondo", "Layana Indah", "Tanamodindi"],
    "Palu Barat": ["Balaroa", "Kamonji", "Lere", "Siranindi"],
    "Palu Selatan": ["Birobuli Selatan", "Birobuli Utara", "Petobo", "Tatura Selatan"],
    "Palu Timur": ["Besusu Barat", "Besusu Tengah", "Besusu Timur", "Lasoani"],
    "Palu Utara": ["Kayumalue Ngapa", "Mamboro", "Taipa"],
    Tatanga: ["Duyu", "Nunu", "Tatura Utara"],
    Tawaeli: ["Baiya", "Lasoani", "Panau"],
    Ulujadi: ["Buluri", "Donggala Kodi", "Silae"],
  };
  const districtSelect = document.getElementById("district-select");
  const neighborhoodSelect = document.getElementById("neighborhood-select");
  if (districtSelect && neighborhoodSelect) {
    const populateNeighborhoods = (preserveSelection) => {
      const previousNeighborhood = preserveSelection ? neighborhoodSelect.value : "";
      neighborhoodSelect.value = "";
      neighborhoodSelect.replaceChildren();
      const selectedDistrict = districtSelect.selectedOptions[0];
      const options = selectedDistrict?.dataset.neighborhoods
        ? selectedDistrict.dataset.neighborhoods.split("|").filter(Boolean)
        : districts[districtSelect.value] || [];
      const placeholder = document.createElement("option");
      placeholder.value = "";
      placeholder.textContent = options.length ? "Pilih kelurahan" : "Pilih kecamatan dahulu";
      neighborhoodSelect.append(placeholder);
      options.forEach((name) => {
        const option = document.createElement("option");
        option.value = name;
        option.textContent = name;
        neighborhoodSelect.append(option);
      });
      if (options.includes(previousNeighborhood)) {
        neighborhoodSelect.value = previousNeighborhood;
      }
      neighborhoodSelect.disabled = options.length === 0;
    };

    districtSelect.addEventListener("change", () => {
      populateNeighborhoods(false);
    });
    if (districtSelect.value) populateNeighborhoods(true);
    window.addEventListener("pageshow", () => {
      if (districtSelect.value) populateNeighborhoods(true);
    });
  }

  const letterForm = document.getElementById("letter-form");
  const previewButton = document.getElementById("preview-draft");
  const previewPanel = document.getElementById("draft-preview");
  const previewError = document.getElementById("draft-error");
  const paper = document.getElementById("draft-paper");
  const backToForm = document.getElementById("back-to-form");
  const downloadDraft = document.getElementById("download-draft");
  if (letterForm && previewButton && previewPanel && previewError && paper) {
    const addRow = (table, label, value) => {
      const row = document.createElement("tr");
      const heading = document.createElement("th");
      const cell = document.createElement("td");
      heading.textContent = label;
      cell.textContent = value || "—";
      row.append(heading, cell);
      table.append(row);
    };

    const renderDraft = (draft) => {
      paper.replaceChildren();
      const heading = document.createElement("header");
      heading.className = "draft-document-header";
      const logo = document.createElement("img");
      logo.className = "draft-emblem";
      logo.src = "/static/lambang-kota-palu.png";
      logo.alt = "Lambang Kota Palu";
      const letterhead = document.createElement("div");
      letterhead.className = "draft-letterhead-text";
      const government = document.createElement("strong");
      government.className = "draft-government-name";
      government.textContent = "PEMERINTAH KOTA PALU";
      const district = document.createElement("strong");
      district.className = "draft-district-name";
      district.textContent = `KECAMATAN ${draft.kecamatan}`;
      const neighborhood = document.createElement("strong");
      neighborhood.className = "draft-office-name";
      neighborhood.textContent = `KELURAHAN ${draft.kelurahan}`;
      const address = document.createElement("span");
      address.className = "draft-address";
      address.textContent = `Alamat: ${draft.alamat_kantor}${draft.email_kantor ? ` | Email: ${draft.email_kantor}` : ""}`;
      letterhead.append(government, district, neighborhood, address);
      heading.append(logo, letterhead);

      const rule = document.createElement("div");
      rule.className = "draft-divider";
      rule.append(document.createElement("span"));
      const title = document.createElement("h1");
      title.textContent = draft.jenis_surat;
      const number = document.createElement("p");
      number.className = "draft-number";
      number.textContent = "Nomor: ____________________";
      const intro = document.createElement("p");
      intro.className = "draft-intro";
      intro.textContent = "Yang bertanda tangan di bawah ini menerangkan bahwa:";
      const table = document.createElement("table");
      table.className = "draft-data-table";
      [
        ["Nama lengkap", draft.nama_lengkap],
        ["NIK", draft.nik],
        ["Tempat / tanggal lahir", draft.tempat_tgl_lahir],
        ["Jenis kelamin", draft.jenis_kelamin],
        ["Pekerjaan", draft.pekerjaan],
        ["Alamat lengkap", draft.alamat_lengkap],
        ["Tujuan / keperluan", draft.keperluan],
      ].forEach(([label, value]) => addRow(table, label, value));
      (draft.data_tambahan || []).forEach((field) => addRow(table, field.label, field.value));

      const closing = document.createElement("p");
      closing.className = "draft-closing";
      closing.textContent = "Demikian surat keterangan ini dibuat sebagai draf untuk diperiksa oleh petugas kelurahan.";
      const signature = document.createElement("section");
      signature.className = "draft-signature";
      const date = document.createElement("p");
      date.textContent = `Palu, ${draft.tanggal_draf}`;
      const official = document.createElement("p");
      official.textContent = "Lurah";
      const officialName = document.createElement("strong");
      officialName.textContent = draft.nama_lurah;
      const nip = document.createElement("p");
      nip.textContent = `NIP: ${draft.nip_lurah}`;
      signature.append(date, official, document.createElement("br"), officialName, nip);
      paper.append(heading, rule, title, number, intro, table, closing, signature);
    };

    previewButton.addEventListener("click", async () => {
      previewError.hidden = true;
      if (!letterForm.reportValidity()) return;

      const data = new FormData(letterForm);
      const additional = {
        kecamatan: data.get("kecamatan"),
        kelurahan: data.get("kelurahan"),
        keperluan: data.get("keperluan"),
        is_self: selfToggle?.checked ? "true" : "false",
      };
      if (selfToggle?.checked) {
        let occupation = String(data.get("applicant_pekerjaan") || "").trim();
        if (!occupation) {
          occupation = (window.prompt("Pekerjaan belum ada di profil. Masukkan pekerjaan untuk melanjutkan:") || "").trim();
          if (!occupation) return;
          const occupationInput = letterForm.querySelector('[name="applicant_pekerjaan"]');
          if (occupationInput) occupationInput.value = occupation;
        }
        additional.pekerjaan = occupation;
      } else {
        [
          "nama",
          "nik",
          "tempat_lahir",
          "tanggal_lahir",
          "jenis_kelamin",
          "pekerjaan",
          "alamat",
        ].forEach((key) => {
          additional[`pemohon_${key}`] = String(data.get(`applicant_${key}`) || "").trim();
        });
        if (!additional.pemohon_pekerjaan) {
          additional.pemohon_pekerjaan = (window.prompt("Pekerjaan pemohon belum diisi. Masukkan pekerjaan untuk melanjutkan:") || "").trim();
          if (!additional.pemohon_pekerjaan) return;
          const occupationInput = letterForm.querySelector('[name="applicant_pekerjaan"]');
          if (occupationInput) occupationInput.value = additional.pemohon_pekerjaan;
        }
      }
      letterForm.querySelectorAll('[name^="specific_"]').forEach((input) => {
        additional[input.name] = input.value.trim();
      });

      previewButton.disabled = true;
      previewButton.textContent = "Menyiapkan pratinjau…";
      try {
        const response = await fetch("/api/v1/letters/draft-preview", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            user_id: letterForm.dataset.userId,
            jenis_surat_id: serviceSelect?.value,
            data_tambahan: additional,
          }),
        });
        const result = await response.json();
        if (!response.ok || result.status !== "success" || !result.draft_data) {
          throw new Error(result.message || "Pratinjau draf tidak dapat dibuat.");
        }
        renderDraft(result.draft_data);
        letterForm.hidden = true;
        document.querySelector(".page-heading")?.setAttribute("hidden", "");
        previewPanel.hidden = false;
        document.body.classList.add("draft-mode");
      } catch (error) {
        previewError.textContent = error instanceof Error ? error.message : "Pratinjau draf tidak dapat dibuat.";
        previewError.hidden = false;
      } finally {
        previewButton.disabled = false;
        previewButton.textContent = "Pratinjau draf";
      }
    });

    backToForm?.addEventListener("click", () => {
      previewPanel.hidden = true;
      letterForm.hidden = false;
      document.querySelector(".page-heading")?.removeAttribute("hidden");
      document.body.classList.remove("draft-mode");
    });
    downloadDraft?.addEventListener("click", () => window.print());
  }
});
