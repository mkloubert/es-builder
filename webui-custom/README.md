# Custom web UI files

Put your own web UI files here. The build copies them into the image, to
`/home/emulationstation/webui-custom/`. Every file you do not give comes from the web
service itself.

```
webui-custom/
  templates/           same names as webui/internal/web/templates/ (only .html)
    dashboard.html     replaces the built-in dashboard page
    partials/my.html   a new partial that your templates can use
  static/              same names as webui/internal/web/static/
    app.css            replaces the built-in stylesheet
    logo.png           a new file at /static/logo.png
```

- Only `templates/` and `static/` are allowed. Symlinks stop the build.
- Tailwind classes in your templates get CSS when the files are here at build time.
- On the device you can change the files over SSH. Click "Reload web UI" on the System page
  (tab "Web UI") to load changed templates. Static files are read on every request.
- If your templates have an error, the web UI uses the built-in templates and the System page
  shows the error.
- A new image replaces the folder on the device with the files from here.
- Templates from before the redesign must be adapted to the new markup and CSS classes.
  `navItem.Icon` now holds a Lucide icon name, so a copied custom layout must use
  `{{icon .Icon}}`. The `icon` function knows only the icons of the built-in sprite
  (`webui/assets/icons.txt`); add more with `webui/scripts/icons.sh`. The layout blocks
  `subtitle` and `actions` are optional.
- See "Custom web UI" in the main `README.md`.
