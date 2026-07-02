// TomaPedidos Print Agent — Web Panel SPA (M5)
// Vanilla JS, no framework. Hash-based routing.

(function () {
  'use strict';

  // ---------- state ----------
  let loggedIn = false;
  let currentView = 'dashboard';
  // Cached data for live refresh
  let healthData = null;
  let printersData = [];
  let configData = null;

  // ---------- helpers ----------
  const $ = (sel, ctx) => (ctx || document).querySelector(sel);
  const $$ = (sel, ctx) => [...(ctx || document).querySelectorAll(sel)];
  const esc = (s) => { const d = document.createElement('div'); d.textContent = s; return d.innerHTML; };

  async function api(method, path, body) {
    const opts = { method, headers: {} };
    if (body) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    const res = await fetch(path, opts);
    if (res.status === 401) { logout(); throw new Error('unauthorized'); }
    if (!res.ok) {
      let msg = res.statusText;
      try { const e = await res.json(); msg = e.message || e.error || msg; } catch (_) {}
      throw new Error(msg);
    }
    return res.json();
  }

  function toast(msg, type) {
    type = type || 'success';
    const el = document.createElement('div');
    el.className = 'toast toast-' + (type === 'warn' ? 'warn' : type === 'error' ? 'error' : 'success');
    el.textContent = msg;
    $('#toast-container').appendChild(el);
    setTimeout(function () { el.remove(); }, 3500);
  }

  // ---------- auth ----------
  function logout() {
    loggedIn = false;
    document.cookie = 'tpd_agent_session=; Max-Age=0; path=/';
    $('#login-screen').classList.remove('hidden');
    $('#main-screen').classList.add('hidden');
  }

  async function doLogin(e) {
    e.preventDefault();
    const pin = $('#pin-input').value;
    try {
      const body = { pin: pin };
      await fetch('/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      loggedIn = true;
      $('#login-screen').classList.add('hidden');
      $('#main-screen').classList.remove('hidden');
      loadDashboard();
      startRefresh();
    } catch (err) {
      $('#login-error').textContent = 'PIN incorrecto';
      $('#login-error').classList.remove('hidden');
    }
  }

  // ---------- refresh / WebSocket ----------
  let ws = null;
  let wsRetryTimer = null;

  function connectWS() {
    if (ws) { try { ws.close(); } catch (_) {} }
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = proto + '//' + location.host + '/events';
    ws = new WebSocket(url);
    ws.onopen = function () {
      // Initial full load via REST, then WS keeps it fresh.
      fetch('/health').then(function (r) { return r.json(); }).then(function (d) { healthData = d; }).catch(function () {});
      fetch('/printers').then(function (r) { return r.json(); }).then(function (d) { printersData = (d.printers || []); }).catch(function () {});
    };
    ws.onmessage = function (e) {
      try {
        var evt = JSON.parse(e.data);
        handleWSEvent(evt);
      } catch (_) {}
    };
    ws.onclose = function () {
      ws = null;
      wsRetryTimer = setTimeout(connectWS, 3000);
    };
    ws.onerror = function () {};
  }

  function handleWSEvent(evt) {
    // Keep health / printer data up to date without a full poll.
    if (evt.type === 'job.printed' || evt.type === 'job.failed' || evt.type === 'job.cancelled') {
      // A job changed state; refresh the jobs view if visible.
      if (currentView === 'jobs') renderJobs();
    }
    if (evt.type === 'printer.status_changed') {
      // Update the local printer list so the dashboard stays current.
      if (healthData && healthData.printers) {
        var hp = healthData.printers.find(function (p) { return p.id === evt.printer; });
        if (hp) hp.status = evt.status;
      }
      if (currentView === 'dashboard') renderDashboard();
    }
    // On any event, refresh the dashboard summary.
    if (evt.type === 'job.queued' || evt.type === 'job.printing') {
      if (currentView === 'dashboard') {
        // Lightweight refresh: re-ask health.
        fetch('/health').then(function (r) { return r.json(); }).then(function (d) { healthData = d; renderDashboard(); }).catch(function () {});
      }
    }
  }

  function startRefresh() {
    if (wsRetryTimer) clearTimeout(wsRetryTimer);
    connectWS();
  }

  // ---------- routing ----------
  function route(view) {
    currentView = view;
    $$('.nav-btn').forEach(function (b) { b.classList.remove('active'); });
    $('[data-route="' + view + '"]').classList.add('active');
    $$('.view').forEach(function (v) { v.classList.add('hidden'); });
    $('#view-' + view).classList.remove('hidden');
    if (view === 'dashboard') loadDashboard();
    if (view === 'printers') loadPrinters();
    if (view === 'jobs') loadJobs();
    if (view === 'settings') loadSettings();
  }

  // ---------- dashboard ----------
  async function loadDashboard() {
    if (!healthData) {
      try { healthData = await api('GET', '/health'); } catch (_) {}
    }
    if (!printersData || printersData.length === 0) {
      try {
        var resp = await api('GET', '/printers');
        printersData = resp.printers || [];
      } catch (_) {}
    }
    renderDashboard();
  }
  function renderDashboard() {
    var v = $('#view-dashboard');
    var h = healthData;
    var ps = printersData;
    v.innerHTML = '<h2 class="card-title" style="margin-bottom:1rem">Dashboard</h2>';

    if (!h) { v.innerHTML += '<p class="text-muted">Cargando...</p>'; return; }

    v.innerHTML += '<div class="grid-2">' + ps.map(function (p) {
      var s = p.status || 'offline';
      var cls = s === 'online' ? 'green' : s === 'error' ? 'amber' : 'red';
      var lp = p.last_print_at && p.last_print_at !== '0001-01-01T00:00:00Z' ? new Date(p.last_print_at).toLocaleTimeString() : '—';
      return '<div class="card"><div class="flex-between"><div><span class="status-dot ' + s + '"></span><strong>' + esc(p.name || p.id) + '</strong> <span class="text-muted">(' + esc(p.type) + ')</span></div><span class="badge badge-' + cls + '">' + esc(s) + '</span></div><div class="mt-2"><span class="text-muted">Cola: </span>' + (p.queue_depth || 0) + '<span class="text-muted" style="margin-left:1rem">Último print: </span>' + lp + '</div>' + (p.last_error ? '<div class="mt-2 text-muted" style="color:#dc2626">' + esc(p.last_error.slice(0,120)) + '</div>' : '') + '</div>';
    }).join('') + '</div>';

    v.innerHTML += '<p class="text-muted mt-2">Uptime: ' + Math.floor((h.uptime_sec || 0) / 60) + 'm · Tenant: ' + esc((h.tenant && h.tenant.id) || '?') + '/' + esc((h.tenant && h.tenant.branch_id) || '?') + '</p>';
  }

  // ---------- printers ----------
  async function loadPrinters() {
    var v = $('#view-printers');
    try { printersData = (await api('GET', '/printers')).printers || []; } catch (_) {}
    renderPrinters(v);
  }

  function renderPrinters(container) {
    var ps = printersData;
    container = container || $('#view-printers');
    container.innerHTML = '<div class="card-header"><h2 class="card-title">Impresoras</h2><div class="flex gap-1"><button class="btn btn-sm" id="detect-btn">Detectar del OS</button><button class="btn btn-primary btn-sm" id="add-printer-btn">+ Agregar</button></div></div>';

    if (ps.length === 0) {
      container.innerHTML += '<div class="card"><p class="text-muted">No hay impresoras configuradas.</p></div>';
    } else {
      container.innerHTML += '<div class="card overflow-x"><table class="data-table"><thead><tr><th>ID</th><th>Nombre</th><th>Tipo</th><th>Estado</th><th>Cola</th><th>Último print</th><th></th></tr></thead><tbody>' + ps.map(function (p) {
        var s = p.status || 'offline';
        var cls = s === 'online' ? 'green' : s === 'error' ? 'amber' : 'red';
        var lp = p.last_print_at && p.last_print_at !== '0001-01-01T00:00:00Z' ? new Date(p.last_print_at).toLocaleString() : '—';
        return '<tr><td class="text-mono">' + esc(p.id) + '</td><td>' + esc(p.name) + '</td><td>' + esc(p.type) + '</td><td><span class="status-dot ' + s + '"></span>' + esc(s) + '</td><td>' + (p.queue_depth || 0) + '</td><td>' + lp + '</td><td><div class="flex gap-1"><button class="btn btn-sm test-print-btn" data-id="' + esc(p.id) + '">Test</button><button class="btn btn-sm edit-printer-btn" data-id="' + esc(p.id) + '">Editar</button><button class="btn btn-sm btn-danger del-printer-btn" data-id="' + esc(p.id) + '">Eliminar</button></div></td></tr>';
      }).join('') + '</tbody></table></div>';
    }

    bindPrinterActions();
  }

  function bindPrinterActions() {
    $('#add-printer-btn').onclick = function () { showPrinterModal(); };
    $('#detect-btn').onclick = detectPrinters;
    $$('.edit-printer-btn').forEach(function (b) { b.onclick = function () { editPrinter(b.dataset.id); }; });
    $$('.del-printer-btn').forEach(function (b) { b.onclick = function () { deletePrinter(b.dataset.id); }; });
    $$('.test-print-btn').forEach(function (b) { b.onclick = function () { testPrint(b.dataset.id); }; });
  }

  async function detectPrinters() {
    try {
      var data = await api('GET', '/printers/detect');
      var raw = data.printers || [];
      if (raw.length === 0) { toast('No se encontraron impresoras en el OS', 'warn'); return; }

      // Normalize: API may return strings (legacy) or objects {name, make_and_model, suggested_type}
      var detected = raw.map(function (p) {
        if (typeof p === 'string') return { name: p, make_and_model: '', suggested_type: 'usb-office' };
        return p;
      });

      var existingNames = {};
      try {
        var cfgResp = await api('GET', '/config');
        (cfgResp.printers || []).forEach(function (p) {
          if (p.system_name) existingNames[p.system_name] = true;
        });
      } catch (_) {}

      var newPrinters = detected.filter(function (d) { return !existingNames[d.name]; });
      if (newPrinters.length === 0) {
        toast('Todas las impresoras detectadas ya están configuradas', 'warn');
        return;
      }

      toast('Encontradas: ' + newPrinters.length + ' impresora(s) nueva(s)', 'success');
      showPrinterModal(null, newPrinters);
    } catch (e) { toast('Error: ' + e.message, 'error'); }
  }

  function showPrinterModal(printer, detectedNames) {
    var isEdit = !!printer;
    var overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.id = 'printer-modal';

    var detectedSection = '';
    if (detectedNames && detectedNames.length > 0) {
      detectedSection =
        '<div class="detected-section mb-2">' +
          '<p class="text-muted mb-2">Impresoras detectadas en el OS:</p>' +
          '<div class="detected-list">' +
            detectedNames.map(function (d, i) {
              var label = d.name;
              if (d.make_and_model) label += ' (' + esc(d.make_and_model) + ')';
              var badge = d.suggested_type === 'usb' ? 'Térmica' : 'Oficina';
              return '<label class="detected-item">' +
                '<input type="radio" name="detected-printer" value="' + i + '" class="detected-radio">' +
                '<span class="detected-name">' + esc(label) + ' <span class="badge badge-sm">' + badge + '</span></span>' +
              '</label>';
            }).join('') +
          '</div>' +
        '</div>';
    }

    overlay.innerHTML = '<div class="modal"><div class="modal-header"><span class="modal-title">' + (isEdit ? 'Editar' : 'Nueva') + ' Impresora</span><button class="modal-close">&times;</button></div>' +
      detectedSection +
      '<div class="form-group"><label class="form-label">ID</label><input class="form-input" id="pf-id" value="' + esc(printer ? printer.id : '') + '" placeholder="cocina, caja, barra..."></div>' +
      '<div class="form-group"><label class="form-label">Nombre</label><input class="form-input" id="pf-name" value="' + esc(printer ? printer.name : '') + '" placeholder="Cocina"></div>' +
      '<div class="form-group"><label class="form-label">Tipo</label><select class="form-select" id="pf-type"><option value="network">network (TCP 9100)</option><option value="usb">usb (spooler)</option><option value="usb-office">usb-office (spooler)</option><option value="file">file (debug)</option></select></div>' +
      '<div id="pf-net"><div class="form-group"><label class="form-label">Host</label><input class="form-input" id="pf-host" value="' + esc(printer ? printer.host || '' : '') + '" placeholder="192.168.1.30"></div><div class="form-group"><label class="form-label">Port</label><input class="form-input" id="pf-port" type="number" value="' + (printer ? printer.port || 9100 : 9100) + '"></div></div>' +
      '<div id="pf-usb" class="hidden"><div class="form-group"><label class="form-label">System Name</label><input class="form-input" id="pf-sysname" value="' + esc(printer ? printer.system_name || '' : '') + '" placeholder="EPSON_TM_T20III"></div></div>' +
      '<div id="pf-file" class="hidden"><div class="form-group"><label class="form-label">File Path</label><input class="form-input" id="pf-filepath" value="' + esc(printer ? printer.file_path || '' : '') + '" placeholder="/tmp/out.bin"></div></div>' +
      '<div class="grid-2"><div class="form-group"><label class="form-label">Code Page</label><select class="form-select" id="pf-cp"><option>cp850</option><option>cp437</option><option>cp860</option><option>cp865</option><option>cp1252</option></select></div><div class="form-group"><label class="form-label">Chars/Line</label><input class="form-input" id="pf-cpl" type="number" value="' + (printer ? printer.chars_per_line || 42 : 42) + '"></div></div>' +
      '<div class="form-group"><label class="form-label">Cut</label><select class="form-select" id="pf-cut"><option value="partial">partial</option><option value="full">full</option><option value="none">none</option></select></div>' +
      '<div class="modal-footer"><button class="btn" id="pf-cancel">Cancelar</button><button class="btn btn-primary" id="pf-save">' + (isEdit ? 'Guardar' : 'Agregar') + '</button></div></div>';
    document.body.appendChild(overlay);

    $('#pf-type').value = printer ? printer.type : 'network';
    if (printer && printer.code_page) $('#pf-cp').value = printer.code_page;
    if (printer && printer.cut) $('#pf-cut').value = printer.cut;
    togglePrinterFields();

    if (detectedNames && detectedNames.length > 0) {
      $$('.detected-radio').forEach(function (radio) {
        radio.addEventListener('change', function () {
          if (this.checked) {
            var idx = parseInt(this.value, 10);
            var d = detectedNames[idx];
            $('#pf-name').value = d.name;
            $('#pf-sysname').value = d.name;
            $('#pf-id').value = d.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
            $('#pf-type').value = d.suggested_type || 'usb-office';
            togglePrinterFields();
          }
        });
      });
    }

    $('#pf-type').onchange = togglePrinterFields;
    $('#pf-cancel').onclick = function () { overlay.remove(); };
    overlay.querySelector('.modal-close').onclick = function () { overlay.remove(); };
    overlay.onclick = function (e) { if (e.target === overlay) overlay.remove(); };

    $('#pf-save').onclick = async function () {
      try {
        var isOffice = $('#pf-type').value === 'usb-office';
        var cfg = await api('GET', '/config');
        var p = {
          id: $('#pf-id').value.trim(),
          name: $('#pf-name').value.trim(),
          type: $('#pf-type').value,
          code_page: isOffice ? '' : $('#pf-cp').value,
          chars_per_line: isOffice ? 0 : (parseInt($('#pf-cpl').value, 10) || 42),
          cut: isOffice ? '' : $('#pf-cut').value
        };
        if (!p.id) return toast('El ID es requerido', 'error');
        if (p.type === 'network') { p.host = $('#pf-host').value.trim(); p.port = parseInt($('#pf-port').value, 10) || 9100; }
        if (p.type === 'usb' || p.type === 'usb-office') { p.system_name = $('#pf-sysname').value.trim(); }
        if (p.type === 'file') { p.file_path = $('#pf-filepath').value.trim(); }

        if (isEdit) {
          var idx = cfg.printers.findIndex(function (x) { return x.id === printer.id; });
          if (idx >= 0) cfg.printers[idx] = p;
        } else {
          cfg.printers.push(p);
        }
        await api('PUT', '/config', cfg);
        toast(isEdit ? 'Impresora actualizada' : 'Impresora agregada', 'success');
        overlay.remove();
        loadPrinters();
      } catch (e) { toast('Error: ' + e.message, 'error'); }
    };
  }

  function togglePrinterFields() {
    var t = $('#pf-type').value;
    $('#pf-net').classList.toggle('hidden', t !== 'network');
    $('#pf-usb').classList.toggle('hidden', t !== 'usb' && t !== 'usb-office');
    $('#pf-file').classList.toggle('hidden', t !== 'file');
    var isOffice = t === 'usb-office';
    $('#pf-cp').closest('.form-group').classList.toggle('hidden', isOffice);
    $('#pf-cpl').closest('.form-group').classList.toggle('hidden', isOffice);
    $('#pf-cut').closest('.form-group').classList.toggle('hidden', isOffice);
  }

  async function editPrinter(id) {
    try { configData = await api('GET', '/config'); } catch (e) { toast('Error al cargar config', 'error'); return; }
    var p = configData.printers.find(function (x) { return x.id === id; });
    if (!p) { toast('No encontrada', 'error'); return; }
    showPrinterModal(p);
  }

  async function deletePrinter(id) {
    if (!confirm('¿Eliminar impresora ' + id + '?')) return;
    try {
      var cfg = await api('GET', '/config');
      cfg.printers = cfg.printers.filter(function (x) { return x.id !== id; });
      await api('PUT', '/config', cfg);
      toast('Eliminada', 'success');
      loadPrinters();
    } catch (e) { toast('Error: ' + e.message, 'error'); }
  }

  async function testPrint(id) {
    try {
      var batchBody = { jobs: [{ printer_id: id, header: { order_number: 0, customer_name: 'TEST PRINT', delivery_type: 'take_away' }, items: [{ qty: 1, name: 'TEST PRINT — OK' }], options: { cut: 'partial' } }] };
      var resp = await api('POST', '/print/batch', batchBody);
      var jobIds = (resp.jobs || []).map(function (j) { return j.job_id; }).join(', ');
      toast('Test print enviado. ' + (jobIds ? 'Job(s): ' + jobIds : ''), 'success');
    } catch (e) { toast('Error: ' + e.message, 'error'); }
  }

  // ---------- jobs ----------
  var jobsFilter = '';

  async function loadJobs() { renderJobs(); }
  async function renderJobs() {
    var v = $('#view-jobs');

    // Fetch ALL jobs first (no filter) to compute the summary counts,
    // then apply the selected filter for the table.
    var allData;
    try { allData = await api('GET', '/jobs'); } catch (_) { allData = { jobs: [] }; }
    var allJobs = allData.jobs || [];
    var counts = { queued: 0, printing: 0, printed: 0, failed: 0, cancelled: 0 };
    allJobs.forEach(function (j) { if (counts[j.status] !== undefined) counts[j.status]++; });

    // Build the filter dropdown, preserving the previous value.
    var filterOptions = [
      { v: '',   label: 'Todos (' + allJobs.length + ')' },
      { v: 'queued',    label: 'Queued (' + counts.queued + ')' },
      { v: 'printing',  label: 'Printing (' + counts.printing + ')' },
      { v: 'printed',   label: 'Printed (' + counts.printed + ')' },
      { v: 'failed',    label: 'Failed (' + counts.failed + ')' },
      { v: 'cancelled', label: 'Cancelled (' + counts.cancelled + ')' },
    ];
    var filterHTML = filterOptions.map(function (o) {
      var sel = jobsFilter === o.v ? ' selected' : '';
      return '<option value="' + o.v + '"' + sel + '>' + o.label + '</option>';
    }).join('');

    v.innerHTML =
      '<div class="card-header"><h2 class="card-title">Jobs</h2><div class="flex gap-1"><button class="btn btn-sm" id="jobs-refresh">Refrescar</button><select class="form-select" id="jobs-filter" style="width:auto">' +
      filterHTML +
      '</select></div></div>';

    // Summary pills
    var pills = [];
    if (counts.queued > 0) pills.push('<span class="badge badge-amber">' + counts.queued + ' en cola</span>');
    if (counts.printing > 0) pills.push('<span class="badge">' + counts.printing + ' imprimiendo</span>');
    if (counts.printed > 0) pills.push('<span class="badge badge-green">' + counts.printed + ' impresos</span>');
    if (counts.failed > 0) pills.push('<span class="badge badge-red">' + counts.failed + ' fallidos</span>');
    if (pills.length > 0) {
      v.innerHTML += '<div class="flex gap-1 mb-2 flex-wrap">' + pills.join('') + '</div>';
    }

    // Filtered list for the table.
    var jobs = jobsFilter
      ? allJobs.filter(function (j) { return j.status === jobsFilter; })
      : allJobs;
    if (jobs.length === 0) {
      v.innerHTML += '<div class="card"><p class="text-muted">No hay jobs' + (jobsFilter ? ' con estado ' + jobsFilter : '') + '.</p></div>';
    } else {
      v.innerHTML += '<div class="card overflow-x"><table class="data-table"><thead><tr><th>ID</th><th>Printer</th><th>Status</th><th>Bytes</th><th>Att</th><th>Created</th><th></th></tr></thead><tbody>' + jobs.map(function (j) {
        var cls = j.status === 'failed' ? 'red' : j.status === 'printed' ? 'green' : j.status === 'cancelled' ? 'amber' : '';
        var ca = j.created_at ? new Date(j.created_at).toLocaleString() : '—';
        var previewBtn = j.preview ? '<button class="btn btn-sm preview-btn" data-id="' + esc(j.id) + '">Ver</button>' : '';
        return '<tr class="job-row" data-id="' + esc(j.id) + '" style="cursor:pointer"><td class="text-mono" style="max-width:100px;overflow:hidden;text-overflow:ellipsis" title="' + esc(j.id) + '">' + esc(j.id.slice(0, 12)) + '…</td><td>' + esc(j.printer_id) + '</td><td><span class="badge badge-' + cls + '">' + esc(j.status) + '</span></td><td>' + (j.bytes || 0) + '</td><td>' + (j.attempts || 0) + '/' + (j.max_attempts || 1) + '</td><td>' + ca + '</td><td><div class="flex gap-1">' + previewBtn + '<button class="btn btn-sm reprint-btn" data-id="' + esc(j.id) + '">Reprint</button><button class="btn btn-sm btn-danger cancel-btn" data-id="' + esc(j.id) + '">Cancel</button></div></td></tr>';
      }).join('') + '</tbody></table></div>';
    }

    $('#jobs-refresh').onclick = function () { renderJobs(); };
    $('#jobs-filter').onchange = function () {
      jobsFilter = this.value;
      renderJobs();
    };
    $$('.reprint-btn').forEach(function (b) { b.onclick = function (e) { e.stopPropagation(); reprintJob(b.dataset.id); }; });
    $$('.cancel-btn').forEach(function (b) { b.onclick = function (e) { e.stopPropagation(); cancelJob(b.dataset.id); }; });
    $$('.preview-btn').forEach(function (b) { b.onclick = function (e) { e.stopPropagation(); showPreview(b.dataset.id); }; });
    $$('.job-row').forEach(function (row) {
      row.onclick = function () {
        var id = row.dataset.id;
        var job = jobs.find(function (j) { return j.id === id; });
        if (job && job.preview) showPreview(id);
      };
    });
  }

  function showPreview(id) {
    var job = null;
    try {
      var allJobs = (JSON.parse(document.querySelector('#view-jobs .data-table') ? '[]' : '[]'));
    } catch (_) {}
    fetch('/jobs/' + id).then(function (r) { return r.json(); }).then(function (j) {
      if (!j.preview) { toast('Sin preview disponible', 'warn'); return; }
      var overlay = document.createElement('div');
      overlay.className = 'modal-overlay';
      overlay.id = 'preview-modal';
      overlay.innerHTML = '<div class="modal" style="max-width:480px"><div class="modal-header"><span class="modal-title">Preview — ' + esc(j.printer_id) + '</span><button class="modal-close">&times;</button></div>' +
        '<div class="card" style="margin:0"><pre style="white-space:pre-wrap;font-family:monospace;font-size:13px;line-height:1.5;margin:0;background:#1a1a2e;color:#e0e0e0;padding:1rem;border-radius:6px;max-height:400px;overflow-y:auto">' + esc(j.preview) + '</pre></div>' +
        '<div class="modal-footer"><button class="btn" id="pv-close">Cerrar</button></div></div>';
      document.body.appendChild(overlay);
      overlay.querySelector('.modal-close').onclick = function () { overlay.remove(); };
      overlay.querySelector('#pv-close').onclick = function () { overlay.remove(); };
      overlay.onclick = function (e) { if (e.target === overlay) overlay.remove(); };
    }).catch(function () { toast('Error al cargar preview', 'error'); });
  }

  async function reprintJob(id) {
    try {
      await api('POST', '/jobs/' + id + '/reprint');
      toast('Job re-encolado', 'success');
      renderJobs();
    } catch (e) { toast('Error: ' + e.message, 'error'); }
  }

  async function cancelJob(id) {
    if (!confirm('¿Cancelar job ' + id + '?')) return;
    try {
      await api('DELETE', '/jobs/' + id);
      toast('Job cancelado', 'success');
      renderJobs();
    } catch (e) { toast('Error: ' + e.message, 'error'); }
  }

  // ---------- settings ----------
  async function loadSettings() { renderSettings(); }
  async function renderSettings() {
    var v = $('#view-settings');
    try { configData = await api('GET', '/config'); } catch (e) { v.innerHTML = '<p class="text-muted">Error al cargar config</p>'; return; }
    v.innerHTML =
      '<h2 class="card-title" style="margin-bottom:1rem">Settings</h2>' +
      '<div class="card"><h3 class="card-title mb-2">PIN del panel</h3><div class="flex gap-1"><input class="form-input" id="s-pin" type="password" maxlength="8" value="' + esc(configData.panel.pin || '') + '" style="width:160px"><button class="btn btn-primary btn-sm" id="s-save-pin">Guardar</button></div></div>' +
      '<div class="card"><h3 class="card-title mb-2">Acerca de</h3><p class="text-muted">TomaPedidos Print Agent · M5</p><p class="text-muted">Versión: ' + esc((healthData && healthData.version) || '?') + '</p><p class="text-muted">Commit: ' + esc((healthData && healthData.commit) || '?') + '</p></div>';

    $('#s-save-pin').onclick = async function () {
      var pin = $('#s-pin').value.trim();
      if (!pin) { toast('El PIN no puede estar vacío', 'error'); return; }
      configData.panel.pin = pin;
      try {
        await api('PUT', '/config', configData);
        toast('PIN actualizado', 'success');
      } catch (e) { toast('Error: ' + e.message, 'error'); }
    };
  }

  // ---------- init ----------
  async function init() {
    $('#login-form').onsubmit = doLogin;
    $('#logout-btn').onclick = logout;
    $$('.nav-btn').forEach(function (b) {
      b.onclick = function () { route(b.dataset.route); };
    });
    // Check if session cookie is still valid.
    try {
      await api('GET', '/config');
      loggedIn = true;
      $('#login-screen').classList.add('hidden');
      $('#main-screen').classList.remove('hidden');
      loadDashboard();
      startRefresh();
    } catch (_) {
      // No valid session — show login screen (default from HTML).
    }
  }

  init();
})();
