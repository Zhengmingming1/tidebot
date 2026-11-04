// Execute the production popup controller against a deterministic DOM contract.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
class Element {
 constructor(tag) { this.tagName=tag.toUpperCase(); this.attrs={}; this.children=[]; this.listeners={}; this.style={}; this.parentElement=null; this.classList={toggle(){}}; this.disabled=false; this.hidden=false; this.scrollHeight=200;this.offsetHeight=200;this.offsetWidth=240; }
 setAttribute(k,v){this.attrs[k]=String(v);} getAttribute(k){return this.attrs[k]??null;} removeAttribute(k){delete this.attrs[k];}
 appendChild(e){this.children.push(e);e.parentElement=this;return e;} remove(){if(this.parentElement)this.parentElement.children=this.parentElement.children.filter(e=>e!==this);this.parentElement=null;}
 contains(e){return this===e||this.children.some(c=>c.contains(e));}
 closest(q){if(q==='select')return this.tagName==='SELECT'?this:null;return null;}
 addEventListener(k,f){(this.listeners[k]??=[]).push(f);} dispatchEvent(e){e.target??=this;for(const f of this.listeners[e.type]||[])f(e);}
 focus(){document.activeElement=this;} scrollIntoView(){} getBoundingClientRect(){return {top:100,bottom:136,left:50,width:200};}
}
class Select extends Element {
 constructor(){super('select');this.options=[];this.multiple=false;this.size=0;this.name='provider';this.labels=[];this._value='';}
 add(value,label,opts={}){const o=new Element('option');Object.assign(o,{value,label},opts);this.options.push(o);this.appendChild(o);return o;}
 set value(v){this._value=v;this.options.forEach(o=>o.selected=o.value===v);}get value(){return this._value;}
 get selectedOptions(){return this.options.filter(o=>o.selected);}
}
const document=new Element('document');document.body=new Element('body');document.appendChild(document.body);document.createElement=t=>new Element(t);
const window=new Element('window');window.innerWidth=1000;window.innerHeight=700;
let observers=[];
class MutationObserver {constructor(fn){this.fn=fn;observers.push(this);}observe(){}disconnect(){this.disconnected=true;}}
class Event {constructor(type,options={}){Object.assign(this,{type},options);}}
const ctx={document,window,MutationObserver,Event,imeKey:e=>e.isComposing,Date,Array,Math};vm.createContext(ctx);vm.runInContext(fs.readFileSync('src/select-controls.js','utf8'),ctx);
function send(target,type,key,extra={}){const e={type,target,key,button:0,isTrusted:true,preventDefault(){this.prevented=true;},stopImmediatePropagation(){this.stopped=true;},...extra};document.dispatchEvent(e);return e;}
function popup(){return document.body.children.find(e=>e.className==='select-popup');}
const s=new Select();document.body.appendChild(s);s.add('old','Unavailable',{hidden:true,disabled:true});s.add('a','Alpha');s.add('off','Disabled',{disabled:true});s.add('b','Beta');s.add('c','Charlie');s.value='old';
assert.equal(ctx.popupSelect(s),true);s.multiple=true;assert.equal(ctx.popupSelect(s),false);s.multiple=false;
let changes=0,inputs=0;s.addEventListener('change',()=>changes++);s.addEventListener('input',e=>{inputs++;assert.equal(e._tbUserSelect,true,'autofill guards recognize a genuine select choice');});
send(s,'pointerdown');assert.ok(popup());assert.equal(popup().children.length,4,'hidden selected option is not offered');assert.equal(s.value,'old','opening must not silently replace unavailable selection');assert.equal(s.getAttribute('aria-expanded'),'true');
assert.ok(send(s,'keydown','ArrowDown').prevented);send(s,'keydown','Enter');assert.equal(s.value,'b','keyboard skips disabled option');assert.equal(changes,1);assert.equal(inputs,1);assert.equal(s._tbTouched,true);assert.equal(popup(),undefined);assert.equal(s.getAttribute('aria-controls'),null);
send(s,'pointerdown');send(s,'keydown','Home');assert.ok(send(s,'keydown','Escape').stopped,'Escape must not dismiss parent dialog');assert.equal(s.value,'b','cancel preserves value');
send(s,'keydown','ArrowDown');send(s,'keydown','c');send(s,'keydown','Enter');assert.equal(s.value,'c','typeahead');
send(s,'pointerdown');send(s,'keydown','Tab');assert.equal(popup(),undefined);assert.equal(s.value,'c');
send(s,'pointerdown');const row=popup().children.find(r=>r.children[1]?.textContent==='Alpha');row.dispatchEvent({type:'click',isTrusted:true});assert.equal(s.value,'a');
send(s,'pointerdown');send(document.body,'pointerdown');assert.equal(popup(),undefined,'outside closes');
send(s,'pointerdown');s.disabled=true;observers.filter(o=>!o.disconnected).forEach(o=>o.fn());assert.equal(popup(),undefined,'disabled closes');s.disabled=false;
send(s,'pointerdown');s.remove();observers.filter(o=>!o.disconnected).forEach(o=>o.fn());assert.equal(popup(),undefined,'detached closes');
console.log('select control behavior passed');
