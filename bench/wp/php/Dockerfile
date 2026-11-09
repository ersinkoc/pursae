# The official WordPress php-fpm image plus the PhpRedis extension, which Redis Object Cache
# recommends over its pure-PHP Predis fallback. The extension is loaded in every setup but only
# used by the tuned one (stock and real-site never connect to Redis).
FROM wordpress:7.1.4-php8.3-fpm
RUN pecl install redis-6.2.0 && docker-php-ext-enable redis
