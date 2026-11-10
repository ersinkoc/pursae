# Publish Notifier

Example plugin for Pursae: the `save_post` action plus `wpgo.HTTPRequest`.

When a post is saved with status `publish`, it POSTs `{"id", "title", "link"}` as JSON to the URL
in the option `publish_notifier_webhook`. Permissions: `posts:read`, `http:outbound`, `options:read`.
