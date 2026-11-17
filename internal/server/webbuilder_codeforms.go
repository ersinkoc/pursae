package server

import (
	"strings"

	"wpgo/internal/webbuilder"
)

// A design's form bound to the Pursae form endpoint (docs/developer/web-builder-code-sections.md,
// "Binding to content"; webbuilder.CompileBinds): the form posts to /forms/{post}/{key} and carries
// data-wb-form (so it gets the nonce, the captcha and the redirect like a builder form) and
// data-wb-code-form. Code pages load none of the builder's runtime, so this small script sends it:
//
//   - the design's own submit handlers run first (its validation shows its own errors). A handler
//     that builds a mailto: link reads the address from a data-* attribute of the form; while the
//     submit is dispatched, reading such an address (form.dataset, getAttribute) stops that handler
//     there and counts as "the design accepted the form", so no mail program opens;
//   - after the handlers (a listener on window, bubble phase) the form is sent with fetch when the
//     design accepted it: it read the address, showed its success element or hid the form, or did
//     not handle the submit at all (then the browser's constraint validation decides); a field the
//     design marked aria-invalid="true" keeps it from being sent;
//   - the answer shows the design's success element (data-wb-form-done) and hides the form, or the
//     design's error element (data-wb-form-error), else a short status line after the form; a
//     redirect configured for the form is followed.
//
// Without JavaScript the form posts as a plain form (303 back with ?wb_form=sent), and the script
// shows the success element when the page comes back.

const wbCodeFormJS = `(function(){var t0=Date.now(),busy=0,read=0;
function q(s,r){return(r||document).querySelector(s)}
function key(f){return f.getAttribute("data-wb-code-form")}
function el(f,k){return q("[data-wb-form-"+k+'="'+key(f)+'"]')}
function shown(e){return!!e&&getComputedStyle(e).display!=="none"&&!e.hidden}
var cls={};function learn(f){if(!(key(f) in cls)){var d=el(f,"done"),e=el(f,"error");cls[key(f)]=!!((d&&d.classList.contains("hidden"))||(e&&e.classList.contains("hidden")))}}
function show(e){if(!e)return;e.hidden=false;e.classList.remove("hidden");var c=e.className;if(!/(^|\s)(flex|grid|block|inline-flex)(\s|$)/.test(c)&&/(^|\s)(flex-col|flex-row|items-|justify-)/.test(c))e.classList.add("flex");if(getComputedStyle(e).display==="none")e.style.display="block"}
function hide(e,f){if(!e)return;if(cls[key(f)]){e.classList.add("hidden");e.classList.remove("flex")}else e.hidden=true}
function status(f,msg,bad){var s=q("[data-wb-code-status]",f.parentNode);if(!s){s=document.createElement("p");s.setAttribute("data-wb-code-status","");f.parentNode.insertBefore(s,f.nextSibling)}s.setAttribute("role",bad?"alert":"status");s.textContent=msg}
function done(f,msg,redirect){var d=el(f,"done");f.setAttribute("data-wb-state","sent");if(d){if(!shown(d)){hide(f,f);show(d)}}else{f.reset();status(f,msg||"")}if(redirect)setTimeout(function(){location.href=redirect},800)}
function fail(f,msg,fields){var d=el(f,"done"),e=el(f,"error");f.setAttribute("data-wb-state","error");if(d&&shown(d)){hide(d,f);f.hidden=false;f.classList.remove("hidden")}
if(fields)for(var n in fields){var i=f.querySelector('[name="'+n+'"]');if(i)i.setAttribute("aria-invalid","true")}
if(e){show(e);if(msg&&!e.textContent.trim())e.textContent=msg}else status(f,msg||"Error",1)}
function send(f){var t=f.querySelector('input[name="wb_t"]');if(t)t.value=String(Date.now()-t0);var e=el(f,"error");if(e&&shown(e))hide(e,f);f.setAttribute("data-wb-state","sending");
fetch(f.action,{method:"POST",body:new FormData(f),headers:{Accept:"application/json"},credentials:"same-origin"}).then(function(r){return r.json().then(function(j){return[r.ok,j]})}).then(function(a){var j=a[1]||{};if(a[0]&&j.ok)done(f,j.message,j.redirect);else fail(f,j.message,j.data&&j.data.fields)},function(){fail(f,"")})}
function addr(f){var out=[];for(var i=0;i<f.attributes.length;i++){var a=f.attributes[i];if(/^data-/.test(a.name)&&/^[^\s@]+@[^\s@]+$/.test(a.value))out.push(a.name)}return out}
function guard(f){var names=addr(f);if(!names.length)return function(){};var ds=f.dataset,ga=f.getAttribute,stop=function(){read=1;throw new Error("wb-form: sent to Pursae")};
var keys=names.map(function(n){return n.slice(5).replace(/-([a-z])/g,function(m,c){return c.toUpperCase()})});
try{Object.defineProperty(f,"dataset",{configurable:true,get:function(){return new Proxy(ds,{get:function(o,k){if(keys.indexOf(k)>=0)stop();return o[k]}})}})}catch(x){}
f.getAttribute=function(n){if(names.indexOf(String(n).toLowerCase())>=0)stop();return ga.call(this,n)};
return function(){delete f.dataset;delete f.getAttribute}}
var undo=null,was=0;
document.addEventListener("submit",function(e){var f=e.target;if(!f||!f.matches||!f.matches("form[data-wb-code-form]"))return;learn(f);busy=1;read=0;was=shown(el(f,"done"));undo=guard(f)},true);
window.addEventListener("error",function(e){if(busy&&e.message&&e.message.indexOf("wb-form: sent to Pursae")>=0)e.preventDefault()});
window.addEventListener("submit",function(e){var f=e.target;if(!f||!f.matches||!f.matches("form[data-wb-code-form]"))return;busy=0;if(undo){undo();undo=null}
var ok;if(!e.defaultPrevented){e.preventDefault();ok=f.noValidate||f.checkValidity();if(!ok&&f.reportValidity)f.reportValidity()}
else if(read)ok=true;else if(f.querySelector('[aria-invalid="true"]'))ok=false;else ok=(!was&&shown(el(f,"done")))||!shown(f);
if(ok)send(f)});
function back(){if(/[?&]wb_form=sent/.test(location.search))document.querySelectorAll("form[data-wb-code-form]").forEach(function(f){learn(f);if(!location.hash||location.hash.slice(1)===key(f))done(f,"")})}
if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",back);else back()
})()`

// wbCodeForms appends the code-form script to a filled snapshot that has a bound design form.
func (s *Server) wbCodeForms(h string) string {
	if !strings.Contains(h, webbuilder.AttrCodeForm+`="`) || strings.Contains(h, `id="wb-code-form"`) {
		return h
	}
	js := `<script` + cspNonceAttr() + ` id="wb-code-form">` + wbCodeFormJS + `</script>`
	// In the code head (lifted into <head>, so the design's markup and the shell it shares with
	// the header and footer stay as they are); the script only listens on document and window.
	const headOpen = `<template data-wb-code-head>`
	if i := strings.Index(h, headOpen); i >= 0 {
		if j := strings.Index(h[i:], `</template>`); j >= 0 {
			return h[:i+j] + js + h[i+j:]
		}
	}
	// Else inside the page's root element: a code page's root is dropped with what follows it
	// (webbuilder.UnwrapCodePage).
	if strings.HasPrefix(strings.TrimSpace(h), `<div`) {
		if i := strings.LastIndex(h, "</div>"); i >= 0 {
			return h[:i] + js + h[i:]
		}
	}
	return h + js
}
