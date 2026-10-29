/*
EmuStation Builder
Copyright (C) 2026  Marcel Joachim Kloubert

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

// Glue between htmx and Alpine: confirmation dialogs and toasts.
(function () {
  function toast(msg) {
    window.dispatchEvent(new CustomEvent("toast", { detail: { value: msg } }));
  }

  // Buttons/forms with hx-confirm open the Alpine modal instead of confirm().
  //
  // The dashboard status block is swapped (outerHTML) every 5 s, which detaches
  // the form that asked for confirmation. Two safeguards: polling is paused
  // while the dialog is open (body.confirming, see layout.html), and if the
  // element is nevertheless gone when the user confirms, the request is
  // re-issued with htmx.ajax (original target and form values) instead of silently doing nothing
  // (htmx aborts issueRequest on detached elements).
  // formValues collects the values of the form a confirmation belongs to, so
  // the re-issued request carries them (repeated names become arrays).
  function formValues(elt) {
    var form = elt.tagName === "FORM" ? elt : elt.closest && elt.closest("form");
    var values = {};
    if (!form) return values;
    new FormData(form).forEach(function (v, k) {
      if (typeof v !== "string") return;
      if (k in values) {
        values[k] = [].concat(values[k], v);
      } else {
        values[k] = v;
      }
    });
    return values;
  }

  document.addEventListener("htmx:confirm", function (evt) {
    if (!evt.detail.question) return;
    evt.preventDefault();
    var d = evt.detail, elt = d.elt;
    // Captured now: the element (and its form) may be gone when the user confirms.
    var values = formValues(elt);
    var target = elt.getAttribute("hx-target") || "body";
    var swap = elt.getAttribute("hx-swap") || (target === "body" ? "none" : "outerHTML");
    window.dispatchEvent(new CustomEvent("ask-confirm", {
      detail: {
        message: d.question,
        proceed: function () {
          if (document.body.contains(elt)) {
            d.issueRequest(true);
          } else {
            htmx.ajax(d.verb || "post", d.path, { source: "body", target: target, swap: swap, values: values });
          }
        },
      },
    }));
  });

  // A 409 carrying HX-Force-Dialog is the "game running" dialog: show it.
  document.addEventListener("htmx:beforeSwap", function (evt) {
    var xhr = evt.detail.xhr;
    if (xhr && xhr.status === 409 && xhr.getResponseHeader("HX-Force-Dialog")) {
      evt.detail.shouldSwap = true;
      evt.detail.isError = false;
      evt.detail.target = document.getElementById("modal-slot");
    }
  });

  // Review page: an "Edit" button enables the inputs of its file. Inputs of
  // ready files start disabled, so the browser does not send them.
  document.addEventListener("click", function (evt) {
    var btn = evt.target.closest && evt.target.closest("[data-edit]");
    if (!btn) return;
    var row = btn.closest("[data-row]");
    if (!row) return;
    row.querySelectorAll("input, select").forEach(function (el) { el.disabled = false; });
    btn.hidden = true;
    var first = row.querySelector("input:not([type=checkbox]), select");
    if (first) first.focus();
  });

  // Network failures.
  document.addEventListener("htmx:sendError", function () {
    toast("Server not reachable.");
  });

  // Error responses: actions send their own toast via HX-Trigger; otherwise fall back.
  document.addEventListener("htmx:responseError", function (evt) {
    var xhr = evt.detail.xhr;
    if (xhr && (xhr.getResponseHeader("HX-Trigger") || xhr.getResponseHeader("HX-Force-Dialog"))) return;
    toast("Action failed (error " + (xhr ? xhr.status : "?") + ").");
  });
})();

// Alpine components.

// uploader: per-file XHR upload with progress; redirects to the review page.
window.uploader = function () {
  return {
    items: [],
    ids: [],
    over: false,
    busy: false,
    finished: false,
    seq: 0,
    get hasError() {
      return this.items.some(function (i) { return i.state === "error"; });
    },
    label: function (it) {
      if (it.state === "uploading" && it.pct >= 100) return "Processing …";
      if (it.state === "uploading") return it.pct + " %";
      return it.state === "error" ? "Error" : it.state === "done" ? "Done" : "Waiting";
    },
    drop: function (e) {
      this.over = false;
      this.pick(e.dataTransfer.files);
    },
    pick: function (files) {
      for (var i = 0; i < files.length; i++) {
        this.items.push({ key: ++this.seq, file: files[i], name: files[i].name, pct: 0, state: "waiting", err: "" });
      }
      this.finished = false;
      this.run();
    },
    run: async function () {
      if (this.busy) return;
      this.busy = true;
      for (var i = 0; i < this.items.length; i++) {
        if (this.items[i].state === "waiting") await this.send(this.items[i]);
      }
      this.busy = false;
      this.finished = true;
      if (this.ids.length && !this.hasError) this.review();
    },
    review: function () {
      location.href = "/upload/" + this.ids.join(",");
    },
    send: function (it) {
      var self = this;
      return new Promise(function (resolve) {
        var xhr = new XMLHttpRequest();
        var fd = new FormData();
        fd.append("file", it.file, it.name);
        it.state = "uploading";
        xhr.open("POST", "/upload");
        xhr.setRequestHeader("Accept", "application/json");
        xhr.upload.onprogress = function (e) {
          if (e.lengthComputable) it.pct = Math.min(100, Math.round((e.loaded / e.total) * 100));
        };
        function fail(msg) {
          it.state = "error";
          it.err = msg;
          resolve();
        }
        xhr.onerror = function () { fail("Server not reachable."); };
        xhr.onabort = function () { fail("Aborted."); };
        xhr.onload = function () {
          var body = {};
          try { body = JSON.parse(xhr.responseText); } catch (e) { /* not JSON */ }
          if (xhr.status === 200 && body.uploads) {
            body.uploads.forEach(function (u) { self.ids.push(u.id); });
            it.pct = 100;
            it.state = "done";
            resolve();
          } else {
            fail(body.error || "Upload failed (error " + xhr.status + ").");
          }
        };
        xhr.send(fd);
      });
    },
  };
};

// jobView: live job progress through Server-Sent Events.
window.jobView = function (ds) {
  var labels = { queued: "Queued", running: "Running …", done: "Done", failed: "Failed", aborted: "Aborted" };
  return {
    status: ds.status,
    progress: parseFloat(ds.progress) || 0,
    err: ds.err || "",
    link: ds.link || "",
    log: ds.log ? ds.log.split("\n") : [],
    get statusText() { return labels[this.status] || this.status; },
    get finished() { return this.status === "done" || this.status === "failed" || this.status === "aborted"; },
    init: function () {
      var self = this;
      if (this.finished) return;
      var es = new EventSource("/jobs/" + ds.id + "/events");
      es.onmessage = function (e) {
        var j = JSON.parse(e.data);
        self.status = j.status;
        self.progress = j.progress || 0;
        self.err = j.err || "";
        self.link = j.link || "";
        self.log = j.log || [];
        self.$nextTick(function () {
          if (self.$refs.log) self.$refs.log.scrollTop = self.$refs.log.scrollHeight;
        });
        if (self.finished) es.close();
      };
      es.onerror = function () {
        if (self.finished) es.close();
      };
    },
  };
};

// btLive: live Bluetooth device list while a scan runs (Server-Sent Events).
window.btLive = function () {
  return {
    scanning: false,
    source: null,
    listen: function () {
      var self = this;
      if (this.source) this.source.close();
      this.scanning = true;
      var es = new EventSource("/bluetooth/events");
      this.source = es;
      es.onmessage = function (e) {
        var el = self.$refs.list;
        if (!el) return;
        el.innerHTML = e.data;
        if (window.htmx) htmx.process(el);
      };
      es.onerror = function () {
        // The server closes the stream when the scan ends; do not reconnect.
        es.close();
        self.source = null;
        self.scanning = false;
        document.body.dispatchEvent(new CustomEvent("bt-updated"));
      };
    },
  };
};
