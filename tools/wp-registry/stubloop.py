import re,subprocess,sys,json
SC,W,F,OUT=sys.argv[1],sys.argv[2],sys.argv[3],sys.argv[4]
fixed={'current_user_can':'return true;','is_admin':'return true;','get_current_user_id':'return 1;','is_multisite':'return false;','wp_using_ext_object_cache':'return false;',
 'wp_cache_get':'return false;','user_can_richedit':'return true;','get_post':'return null;','admin_url':'return "/wp-admin/".ltrim($a[0]??"","/");','includes_url':'return "/wp-includes/".ltrim($a[0]??"","/");',
 'home_url':'return "HOMEURL".($a[0]??"");','site_url':'return "SITEURL".($a[0]??"");','rest_url':'return "/wp-json/".ltrim($a[0]??"","/");','wp_create_nonce':'return "NONCE:".($a[0]??"");',
 'get_locale':'return "en_US";','determine_locale':'return "en_US";','get_user_locale':'return "en_US";','is_rtl':'return false;','wp_is_mobile':'return false;','is_ssl':'return false;','wp_installing':'return false;',
 'wp_convert_hr_to_bytes':'return 67108864;','wp_max_upload_size':'return 67108864;','get_bloginfo':'return "7.1.3";','wp_get_wp_version':'return "7.1.3";'}
for i in range(400):
    src=open(F).read()
    r=subprocess.run(['php',F,W],capture_output=True,text=True)
    err=r.stderr+r.stdout[:3000]
    m=re.search(r'undefined function ([A-Za-z_0-9]+)',err)
    if m:
        f=m.group(1); body=fixed.get(f) or 'return null;'
        src=src.replace("//MARK","function %s(...$a){%s}\n//MARK"%(f,body),1)
        open(F,'w').write(src); continue
    m=re.search(r'Undefined constant "([A-Z_]+)"',err)
    if m:
        src=src.replace("//MARK","define('%s','');\n//MARK"%m.group(1),1)
        open(F,'w').write(src); print('const',m.group(1)); continue
    m=re.search(r'Cannot redeclare function ([A-Za-z_0-9]+)',err)
    if m:
        new=re.sub(r'function %s\(.*?\)\{.*?\}\n'%m.group(1),'',src,count=1,flags=re.S)
        if new==src: print('cannot unstub',m.group(1)); sys.exit(1)
        open(F,'w').write(new); print('unstub',m.group(1)); continue
    m=re.search(r'Fatal error: (.*)',err)
    if m: print('FATAL',m.group(1)[:600]); sys.exit(1)
    open(OUT,'w').write(r.stdout); print('ok after',i); break
