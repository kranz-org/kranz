# Documentation deployment

The repository workflow publishes this VitePress project to:

`https://kranz-org.github.io/kranz/`

The site uses `/kranz/` as its VitePress base path. In the GitHub repository,
select **Settings → Pages → Source → GitHub Actions** once. Pull requests
build the site without deploying it; pushes to `main` deploy it.

## Organization-root redirect

GitHub Pages serves `https://kranz-org.github.io/` from the special repository
`kranz-org/kranz-org.github.io`. That repository does not exist yet, so the main
Kranz repository cannot provide the root redirect by itself.

When the organization-root repository is created, add this `index.html` and
enable Pages from its default branch:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta http-equiv="refresh" content="0; url=https://kranz-org.github.io/kranz/">
    <link rel="canonical" href="https://kranz-org.github.io/kranz/">
    <title>Kranz documentation</title>
    <script>
      location.replace('https://kranz-org.github.io/kranz/' + location.search + location.hash)
    </script>
  </head>
  <body>
    <a href="https://kranz-org.github.io/kranz/">Open Kranz documentation</a>
  </body>
</html>
```

The redirect preserves query parameters and the URL fragment. A project Pages
site cannot claim or redirect the organization-root URL.
