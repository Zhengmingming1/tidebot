  // ---------- reusable skill compositions ----------
  var skillEditorSeq = 0;
  function skillTabs(active) {
    return sectionTabs("ss-tabs", T("ss.tabs"), [["skills", "nav.skills"], ["skill-repositories", "sr.title"], ["skill-sets", "ss.title"], ["workflows", "ss.workflows"]].map(function (x) {
      return { href: "#/" + x[0], label: T(x[1]), active: active === x[0] };
    }));
  }
  function skillBindingEditor(box, skills, initial, onChange, options) {
    options = options || {}; var source = cleanSetSource(options.source);
    var uid = "ss-role-" + (++skillEditorSeq), bindings = [], versions = {}, loading = {}, sets = [];
    function normalize(bs) {
      var out = (bs || []).map(function (b) { return { skill_id: b.skill_id, skill_name: b.skill_name || "", role: b.role || "", mode: b.mode || (b.version_id ? "pinned" : "follow"), version_id: b.version_id || "" }; });
      if (out.length && !out.some(function (b) { return b.role === "primary"; })) out[0].role = "primary";
      out.forEach(function (b) { if (!b.role) b.role = "auxiliary"; if (b.mode !== "pinned") b.version_id = ""; });
      return out.filter(function (b) { return b.role === "primary"; }).concat(out.filter(function (b) { return b.role !== "primary"; }));
    }
    function value() { return bindings.map(function (b) { return Object.assign({}, b); }); }
    function changed() { bindings = normalize(bindings); paint(); if (onChange) onChange(value(), cleanSetSource(source)); box.dispatchEvent(new Event("input", { bubbles: true })); }
    function paint() {
      var available = skills.filter(function (s) { return !bindings.some(function (b) { return b.skill_id === s.id; }); });
      box.innerHTML = '<div class="ss-editor"><p class="hint">' + esc(T("ss.editor_hint")) + '</p>' +
        (source ? '<p class="notice ss-source">' + esc(T("ss.source_fixed")) + ' <code>' + esc(source.version_id) + '</code><button type="button" class="btn sm" data-detach>' + esc(T("ss.detach")) + '</button></p>' : "") +
        (bindings.length ? '<div class="ss-members">' + bindings.map(function (b, i) {
          var skill = skills.filter(function (s) { return s.id === b.skill_id; })[0], vs = versions[b.skill_id] || [], missingVersion = b.version_id && !vs.some(function (v) { return v.id === b.version_id; });
          var opts = opt("", T(options.freeze ? "ss.freeze_current" : "bt.latest"), b.mode !== "pinned");
          if (missingVersion) opts += opt(b.version_id, T("ss.pinned") + " · " + b.version_id, true);
          opts += vs.map(function (v) { return opt(v.id, T("bt.pin_v", { n: v.no }), b.mode === "pinned" && b.version_id === v.id); }).join("");
          return '<div class="ss-member" data-member="' + i + '"><label class="ss-role"><input type="radio" name="' + uid + '" data-role="' + i + '"' + (b.role === "primary" ? " checked" : "") + '><span>' + esc(T(b.role === "primary" ? "ss.primary" : "ss.auxiliary")) + '</span></label><div class="ss-member-name"><strong>' + esc(skill ? skill.name : b.skill_name || b.skill_id) + '</strong>' + (!skill ? '<small class="field-err">' + esc(T("ss.missing")) + '</small>' : "") + '</div><select data-version="' + i + '" aria-label="' + esc(T("bt.skill_ver") + " " + (skill ? skill.name : b.skill_id)) + '">' + opts + '</select><button type="button" class="icon-btn sm" data-remove="' + i + '" aria-label="' + esc(T("ss.remove")) + '">' + ic("x", "sm") + '</button></div>';
        }).join("") + '</div>' : '<p class="muted">' + esc(T("ss.no_members")) + '</p>') +
        '<div class="ss-add"><select data-add aria-label="' + esc(T("ss.add")) + '"' + (bindings.length >= 16 ? " disabled" : "") + '>' + opt("", T("ss.add"), true) + available.map(function (s) { return opt(s.id, s.name, false); }).join("") + '</select><select data-set aria-label="' + esc(T("ss.use_set")) + '">' + opt("", T("ss.use_set"), true) + sets.map(function (s) { return opt(s.id, s.name + " · v" + s.version_no, false); }).join("") + '</select></div><button type="button" class="btn sm" data-resolve>' + esc(T("ss.resolve")) + '</button> <button type="button" class="btn sm" data-context>' + esc(T("dep.local")) + '</button></div>';
      $('[data-context]', box).onclick = function () { dependencyCheck(options.checkRequest ? options.checkRequest() : {kind:"draft",skill_bindings:value(),mode:"read"}, true); };
      $('[data-resolve]', box).onclick = function () { previewSkillDependencies(value(), function (bs) { bindings = normalize(bs); changed(); }, value); };
      var detach = $('[data-detach]', box); if (detach) detach.onclick = function () { source = null; changed(); };
      $('[data-add]', box).addEventListener("change", function (e) { var sk = skills.filter(function (s) { return s.id === e.target.value; })[0]; if (!sk || bindings.length >= 16) return; bindings.push({ skill_id: sk.id, skill_name: sk.name, role: bindings.length ? "auxiliary" : "primary", mode: "follow", version_id: "" }); changed(); });
      $('[data-set]', box).addEventListener("change", function (e) { var set = sets.filter(function (s) { return s.id === e.target.value; })[0]; if (set) { bindings = normalize(set.skill_bindings); source = { set_id: set.id, version_id: set.version_id }; changed(); } });
      $$('[data-role]', box).forEach(function (r) { r.addEventListener("change", function () { var n = +r.getAttribute("data-role"); bindings.forEach(function (b, i) { b.role = i === n ? "primary" : "auxiliary"; }); changed(); }); });
      $$('[data-remove]', box).forEach(function (b) { b.addEventListener("click", function () { bindings.splice(+b.getAttribute("data-remove"), 1); changed(); }); });
      $$('[data-version]', box).forEach(function (v) { v.addEventListener("change", function () { var b = bindings[+v.getAttribute("data-version")]; b.mode = v.value ? "pinned" : "follow"; b.version_id = v.value; changed(); }); });
      bindings.forEach(function (b) {
        if (versions[b.skill_id] || loading[b.skill_id] || !skills.some(function (s) { return s.id === b.skill_id; })) return;
        loading[b.skill_id] = true;
        api("/api/skills/" + enc(b.skill_id) + "/versions").then(function (v) { versions[b.skill_id] = (v.versions || []).slice().reverse(); if (document.contains(box)) paint(); }).catch(function () { versions[b.skill_id] = []; });
      });
    }
    bindings = normalize(initial); paint();
    api("/api/skill-sets").then(function (v) { sets = v || []; if (document.contains(box)) paint(); }).catch(function () {});
    return { value: value, source: function () { return cleanSetSource(source); }, set: function (bs, src) { bindings = normalize(bs); source = cleanSetSource(src); changed(); } };
  }
  function cleanSetSource(source) {
    return source && source.set_id && source.version_id ? {set_id:source.set_id,version_id:source.version_id} : null;
  }
  function dependencyPreviewHtml(p) {
    return '<p class="notice '+(p.ready?'ok':p.resolvable?'':'bad')+'">'+esc(T(p.ready?'ss.deps_ready':p.resolvable?'ss.deps_add':'ss.deps_blocked'))+'</p>'+
      (p.additions||[]).map(function(b){return '<p>＋ '+esc(b.skill_name||b.skill_id)+' <code>'+esc(b.version_id)+'</code></p>';}).join('')+
      (p.issues||[]).map(function(i){return '<p class="notice '+(i.optional?'':'bad')+'"><strong>'+esc(i.skill_name)+'</strong> · '+esc(T(i.optional?'ss.optional':'ss.required'))+'<br>'+esc(i.message)+'</p>';}).join('');
  }
  function previewSkillDependencies(bindings, apply, current) {
    var snapshot=JSON.stringify(bindings), panel=openDrawer(function(dw){
      dw.innerHTML='<div class="drawer-head"><h2>'+esc(T('ss.resolve'))+'</h2><button class="icon-btn" data-dismiss aria-label="'+esc(T('btn.close'))+'">'+ic('x')+'</button></div><div class="drawer-body"><p class="hint">'+esc(T('ss.resolve_hint'))+'</p><div data-preview>'+esc(T('dep.starting'))+'</div></div>';
      $('[data-dismiss]',dw).onclick=closeDrawer;
    });
    api('/api/skill-sets/preview',{method:'POST',body:{skill_bindings:bindings}}).then(function(p){
      if(!document.contains(panel))return;var box=$('[data-preview]',panel);
      box.innerHTML=dependencyPreviewHtml(p)+(p.resolvable&&p.additions.length?'<button class="btn primary" data-adopt>'+esc(T('ss.adopt'))+'</button>':'');
      var b=$('[data-adopt]',box);if(b)b.onclick=function(){if(current&&JSON.stringify(current())!==snapshot){toast(T('ss.preview_stale'),true);return;}apply(p.bindings);closeDrawer();};
    }).catch(function(e){if(document.contains(panel))$('[data-preview]',panel).textContent=e.message;});
  }
  function skillSetModal(set, skills) {
    set=set||{name:'',skill_bindings:[]};var details=set.details||{},cases=JSON.parse(JSON.stringify(details.test_cases||[]));
    function textField(name,key,rows){return fieldHtml(T(key),'<textarea name="'+name+'" rows="'+rows+'">'+esc(details[name]||'')+'</textarea>');}
    modal('<div class="card-head"><h2>'+esc(T(set.id?'ss.edit':'ss.new'))+'</h2></div><form id="ss-form">'+fieldHtml(T('tf.name'),'<input name="name" required maxlength="80" value="'+esc(set.name)+'">')+
      textField('description','ss.description',2)+'<div id="ss-members"></div><p class="hint">'+esc(T('ss.freeze_hint'))+'</p>'+
      '<details class="ss-details"><summary>'+esc(T('ss.docs'))+'</summary><p class="hint">'+esc(T('ss.docs_hint'))+'</p>'+textField('instructions','ss.instructions',3)+textField('input_hint','ss.input_hint',2)+textField('output_hint','ss.output_hint',2)+'</details>'+
      '<details class="ss-details"'+(cases.length?' open':'')+'><summary>'+esc(T('ss.test_cases'))+'</summary><p class="hint">'+esc(T('ss.test_hint'))+'</p><div id="ss-cases"></div><button type="button" class="btn sm" id="ss-add-case">'+ic('plus','sm')+esc(T('ss.add_case'))+'</button></details>'+
      '<p id="ss-error" class="field-err" role="alert"></p><div class="actions"><button type="button" class="btn ghost" data-close>'+esc(T('btn.cancel'))+'</button><button type="button" class="btn" id="ss-dependencies">'+esc(T('ss.dependencies'))+'</button><button type="submit" class="btn primary">'+esc(T('btn.save'))+'</button></div></form>',
      function(root,close){
        var f=$('#ss-form',root),editor=skillBindingEditor($('#ss-members',root),skills,set.skill_bindings,null,{freeze:true});
        function paintCases(){
          $('#ss-cases',root).innerHTML=cases.map(function(c,i){
            return '<fieldset class="ss-case" data-case="'+i+'"><legend>'+esc(T('ss.case_n',{n:i+1}))+'</legend>'+fieldHtml(T('tf.name'),'<input data-case-field="name" required maxlength="80" value="'+esc(c.name||'')+'">')+
              fieldHtml(T('wf.input'),'<textarea data-case-field="input" required rows="3">'+esc(c.input||'')+'</textarea>')+
              fieldHtml(T('ss.must_contain'),'<textarea data-case-field="must_contain" rows="2">'+esc((c.must_contain||[]).join('\n'))+'</textarea>')+
              fieldHtml(T('ss.must_not_contain'),'<textarea data-case-field="must_not_contain" rows="2">'+esc((c.must_not_contain||[]).join('\n'))+'</textarea>')+
              '<button type="button" class="btn sm ghost" data-delete-case>'+esc(T('ss.remove'))+'</button></fieldset>';
          }).join('');
          $$('[data-case]',root).forEach(function(row){var i=+row.dataset.case;
            $$('[data-case-field]',row).forEach(function(el){el.oninput=function(){var k=el.dataset.caseField;cases[i][k]=k==='name'||k==='input'?el.value:el.value.split('\n').map(function(x){return x.trim();}).filter(Boolean);};});
            $('[data-delete-case]',row).onclick=function(){cases.splice(i,1);paintCases();};
          });
          $('#ss-add-case',root).disabled=cases.length>=5;
        }
        paintCases();$('#ss-add-case',root).onclick=function(){if(cases.length<5){cases.push({name:'',input:'',must_contain:[],must_not_contain:[]});paintCases();}};
        $('#ss-dependencies',root).onclick=function(){dependencyCheck({kind:'draft',skill_bindings:editor.value(),prompt:'',provider:''});};
        f.onsubmit=function(e){e.preventDefault();var bs=editor.value(),err=$('#ss-error',root);err.textContent='';
          if(bs.length<2){err.textContent=T('ss.need_two');return;}
          var btn=$('button[type=submit]',f);btn.disabled=true;
          var body={name:f.elements.name.value.trim(),skill_bindings:bs,expected_version_id:set.version_id||'',details:{description:f.description.value,instructions:f.instructions.value,input_hint:f.input_hint.value,output_hint:f.output_hint.value,test_cases:cases}};
          api('/api/skill-sets'+(set.id?'/'+enc(set.id):''),{method:set.id?'PUT':'POST',body:body}).then(function(){close();toast(T('ss.saved'));renderSkillSets();})
            .catch(function(e){btn.disabled=false;err.textContent=e.message;if(e.data&&e.data.dependency_preview)previewSkillDependencies(bs,function(full){editor.set(full);},editor.value);});
        };
      });
  }
  function skillSetUsage(set) {
    var generation=0,panel=openDrawer(function(dw){
      dw.innerHTML='<div class="drawer-head"><h2>'+esc(T('ss.usage'))+'</h2><button class="icon-btn" data-dismiss aria-label="'+esc(T('btn.close'))+'">'+ic('x')+'</button></div><div class="drawer-body"><p class="hint">'+esc(T('ss.upgrade_hint'))+'</p><div data-usage>'+esc(T('dep.starting'))+'</div></div>';
      $('[data-dismiss]',dw).onclick=closeDrawer;
    });
    function load(version){
      var gen=++generation;
      $$('[data-target],[data-apply]',panel).forEach(function(el){el.disabled=true;});
      api('/api/skill-set-usage?set_id='+enc(set.id)+(version?'&target_version_id='+enc(version):'')).then(function(r){
        if(!document.contains(panel)||gen!==generation)return;var host=$('[data-usage]',panel);
        host.innerHTML=fieldHtml(T('ss.target_version'),'<select data-target>'+r.versions.slice().reverse().map(function(v){return opt(v.version_id,'v'+v.version_no+(v.current?' · '+T('col.current'):''),v.version_id===r.target_version_id);}).join('')+'</select>')+
          (!r.usages.length?'<p>'+esc(T('ss.no_usage'))+'</p>':r.usages.map(function(u,i){
            return '<article class="dep-finding" data-usage-id="'+esc(u.id)+'" data-usage-node="'+esc(u.node_id||'')+'"><div class="row-actions"><strong>'+esc(u.name)+'</strong><span class="pill">'+esc(T('ss.state_'+u.state))+'</span></div><p class="hint">'+esc(T(u.kind==='task'?'col.task':'wf.skill_step'))+' · <code>'+esc(u.source.version_id)+'</code></p>'+
              (u.changes||[]).map(function(c){var b=c.before||{},a=c.after||{};return '<p>'+esc(T('ss.change_'+c.kind))+' · '+esc(a.skill_name||b.skill_name||c.skill_id)+'<br><code>'+esc(b.skill_id?T(b.role==='primary'?'ss.primary':'ss.auxiliary')+' · '+T(b.mode==='follow'?'bt.latest':'ss.pinned')+' · '+(b.version_id||'—'):'—')+' → '+esc(a.skill_id?T(a.role==='primary'?'ss.primary':'ss.auxiliary')+' · '+T(a.mode==='follow'?'bt.latest':'ss.pinned')+' · '+(a.version_id||'—'):'—')+'</code></p>';}).join('')+
              (u.can_apply?'<button class="btn sm" data-apply="'+i+'">'+esc(T('ss.apply_version'))+'</button>':'<p class="hint">'+esc(T(u.state==='linked'?'ss.already_version':'ss.keep_local'))+'</p>')+'</article>';
          }).join(''));
        $('[data-target]',host).onchange=function(){load(this.value);};
        $$('[data-apply]',host).forEach(function(b){b.onclick=function(){
          var u=r.usages[+b.dataset.apply];$$('[data-target],[data-apply]',panel).forEach(function(el){el.disabled=true;});
          api('/api/skill-set-usage/apply',{method:'POST',body:{target:u.kind,id:u.id,node_id:u.node_id,expected_version_id:u.expected_version_id,expected_version:u.expected_version,target_set_version_id:r.target_version_id}})
            .then(function(){toast(T('ss.applied'));load(r.target_version_id);}).catch(function(e){$$('[data-target],[data-apply]',panel).forEach(function(el){el.disabled=false;});fail(e);});
        };});
      }).catch(function(e){if(document.contains(panel)&&gen===generation)$('[data-usage]',panel).textContent=e.message;});
    }
    load();
  }
  function skillManifestModal(id) {
    Promise.all([api('/api/skill-manifests/'+enc(id)),api('/api/skills')]).then(function(r){
      var data=r[0],library=r[1],manifest=JSON.parse(JSON.stringify(data.manifest||{}));manifest.format='tidebot-skill/1';manifest.dependencies=manifest.dependencies||[];manifest.environment=manifest.environment||[];
      modal('<div class="card-head"><h2>'+esc(T('sm.title'))+'</h2></div><form id="sm-form"><p class="hint">'+esc(T('sm.hint'))+'</p><p>'+esc(T('sm.canonical'))+' <code>'+esc(data.canonical_name||id)+'</code></p><h3>'+esc(T('sm.skill_deps'))+'</h3><div id="sm-deps"></div><div class="row-actions"><select id="sm-library" aria-label="'+esc(T('ss.add'))+'">'+opt('',T('ss.add'),true)+library.filter(function(s){return s.id!==id;}).map(function(s){return opt(s.id,s.name,false);}).join('')+'</select><button type="button" class="btn sm" id="sm-manual">'+esc(T('sm.manual'))+'</button></div><h3>'+esc(T('sm.environment'))+'</h3><p class="hint">'+esc(T('sm.env_hint'))+'</p><div id="sm-env"></div><button type="button" class="btn sm" id="sm-add-env">'+esc(T('sm.add_env'))+'</button><p class="field-err" id="sm-error" role="alert"></p><div class="actions"><button class="btn ghost" type="button" data-close>'+esc(T('btn.cancel'))+'</button><button class="btn primary" type="submit">'+esc(T('sm.save'))+'</button></div></form>',function(root,close){
        function paint(){
          $('#sm-deps',root).innerHTML=manifest.dependencies.map(function(d,i){
            return '<fieldset class="ss-case" data-dep-row="'+i+'">'+fieldHtml(T('sm.skill_name'),'<input data-key="skill_name" required value="'+esc(d.skill_name||'')+'">')+
              fieldHtml(T('sm.hash'),'<input data-key="content_sha256" class="mono" maxlength="64" value="'+esc(d.content_sha256||'')+'">')+
              '<div class="row-actions"><label><input type="checkbox" data-key="optional"'+(d.optional?' checked':'')+'> '+esc(T('ss.optional'))+'</label><button type="button" class="btn sm ghost" data-remove-row>'+esc(T('ss.remove'))+'</button></div></fieldset>';
          }).join('');
          $('#sm-env',root).innerHTML=manifest.environment.map(function(d,i){
            return '<fieldset class="ss-case" data-env-row="'+i+'">'+fieldHtml(T('sm.kind'),'<select data-key="kind">'+['command','python_package','env','tool','mcp','file'].map(function(k){return opt(k,T('sm.kind_'+k),k===d.kind);}).join('')+'</select>')+
              fieldHtml(T('sm.requirement'),'<input data-key="name" required value="'+esc(d.name||'')+'">')+fieldHtml(T('sm.version'),'<input data-key="version" value="'+esc(d.version||'')+'">')+
              '<div class="row-actions"><label><input type="checkbox" data-key="optional"'+(d.optional?' checked':'')+'> '+esc(T('ss.optional'))+'</label><button type="button" class="btn sm ghost" data-remove-row>'+esc(T('ss.remove'))+'</button></div></fieldset>';
          }).join('');
          [['dep','dependencies'],['env','environment']].forEach(function(pair){$$('[data-'+pair[0]+'-row]',root).forEach(function(row){var i=+row.getAttribute('data-'+pair[0]+'-row'),list=manifest[pair[1]];
            $$('[data-key]',row).forEach(function(el){el.oninput=function(){list[i][el.dataset.key]=el.type==='checkbox'?el.checked:el.value;};});
            $('[data-remove-row]',row).onclick=function(){list.splice(i,1);paint();};
          });});
          $('#sm-add-env',root).disabled=manifest.environment.length>=32;$('#sm-manual',root).disabled=manifest.dependencies.length>=16;
        }
        paint();
        $('#sm-library',root).onchange=function(){var pick=this,skill=pick.value;if(!skill||manifest.dependencies.length>=16)return;pick.disabled=true;
          api('/api/skill-manifests/'+enc(skill)).then(function(m){if(!document.contains(root))return;manifest.dependencies.push({skill_name:m.canonical_name,content_sha256:m.content_sha256,optional:false});paint();}).catch(fail).finally(function(){pick.disabled=false;pick.value='';});
        };
        $('#sm-manual',root).onclick=function(){if(manifest.dependencies.length<16){manifest.dependencies.push({skill_name:'',optional:false});paint();}};
        $('#sm-add-env',root).onclick=function(){if(manifest.environment.length<32){manifest.environment.push({kind:'command',name:'',version:'',optional:false});paint();}};
        $('#sm-form',root).onsubmit=function(e){e.preventDefault();var btn=$('button[type=submit]',root);btn.disabled=true;
          api('/api/skill-manifests/'+enc(id),{method:'PUT',body:{expected_version_id:data.version_id,manifest:manifest}}).then(function(){close();toast(T('sm.saved'));refresh();}).catch(function(e){btn.disabled=false;$('#sm-error',root).textContent=e.message;});
        };
      });
    }).catch(fail);
  }
  function skillSetEvaluation(set) {
    api('/api/settings').then(function(settings){
      var selected='',generation=0,count=((set.details||{}).test_cases||[]).length;
      var panel=openDrawer(function(dw){
        dw.innerHTML='<div class="drawer-head"><h2>'+esc(T('ss.evaluate'))+' · '+esc(set.name)+'</h2><button class="icon-btn" data-dismiss aria-label="'+esc(T('btn.close'))+'">'+ic('x')+'</button></div><div class="drawer-body"><p class="notice">'+esc(T('ev.scope'))+'</p><p>'+esc(T('ev.calls',{n:count*2}))+' · v'+esc(set.version_no)+'</p>'+
          '<details id="ev-config" open><summary>'+esc(T('ev.options'))+'</summary><form id="ev-start">'+fieldHtml(T('f.provider'),'<select name="provider">'+providerOptions(settings, smartProvider(settings), false, false)+'</select>')+
          fieldHtml(T('ev.budget'),'<input name="budget" type="number" min="0.02" max="5" step="0.01" value="1" required>')+
          '<button type="submit" class="btn primary"'+(!count?' disabled':'')+'>'+esc(T('ev.start'))+'</button>'+(!count?'<p class="hint">'+esc(T('ev.no_cases'))+'</p>':'')+'</form></details><p class="field-err" id="ev-error" role="alert"></p>'+
          '<h3>'+esc(T('ev.history'))+'</h3><select id="ev-history" aria-label="'+esc(T('ev.history'))+'"></select><div id="ev-result" aria-live="polite"></div></div>';
        $('[data-dismiss]',dw).onclick=closeDrawer;
        $('#ev-history',dw).onchange=function(){selected=this.value;++generation;if(selected){$('#ev-config',panel).open=false;poll(generation);}else $('#ev-result',panel).innerHTML='';};
        $('#ev-start',dw).onsubmit=function(e){e.preventDefault();var f=this;if(!readyProviders(settings).some(function(p){return p.name===f.provider.value;})){$('#ev-error',dw).textContent=T('cc.no_provider');return;}var btn=$('button',f);btn.disabled=true;$('#ev-error',dw).textContent='';
          api('/api/skill-set-evals',{method:'POST',body:{set_id:set.id,version_id:set.version_id,provider:f.provider.value,budget_usd:Number(f.budget.value)}}).then(function(r){selected=r.id;$('#ev-config',panel).open=false;history();poll(++generation);}).catch(function(e){$('#ev-error',dw).textContent=e.message;}).finally(function(){btn.disabled=!count;});
        };
      });
      function history(){return api('/api/skill-set-evals?set_id='+enc(set.id)).then(function(items){
        if(!document.contains(panel))return;$('#ev-history',panel).innerHTML=opt('',T('ev.select'),!selected)+items.map(function(r){return opt(r.id,when(r.created_at)+' · '+T('ev.status_'+r.status),r.id===selected);}).join('');
      }).catch(function(e){if(document.contains(panel))$('#ev-error',panel).textContent=e.message;});}
      function outcome(x,key){
        if(!x)return '<section class="ss-eval-outcome"><h4>'+esc(T(key))+'</h4><p>'+esc(T('ev.pending'))+'</p></section>';
        return '<section class="ss-eval-outcome"><h4>'+esc(T(key))+'</h4><p><span class="pill '+(x.status==='succeeded'?(x.passed?'ok':'warn'):'')+'">'+esc(x.status==='succeeded'?T(x.passed?'ev.passed':'ev.not_passed'):T('wf.status_'+x.status))+'</span></p>'+
          '<p class="hint">'+esc(dur(x.duration_ms||0))+' · '+esc(money(x.cost_usd||0,'USD',4))+' · '+esc((x.input_tokens||0)+' / '+(x.output_tokens||0))+' tokens</p>'+
          (x.error?'<p class="notice bad">'+esc(x.error)+'</p>':'')+
          (x.assertions||[]).map(function(a){return '<p>'+ic(a.passed?'check':'x','sm')+' '+esc(T(a.kind==='must_not_contain'?'ss.must_not_contain_short':'ss.must_contain_short'))+' <code>'+esc(a.text)+'</code></p>';}).join('')+
          (x.output?'<details open><summary>'+esc(T('wf.output'))+'</summary><pre class="wf-output">'+esc(x.output)+'</pre></details>':'')+
          (x.output_truncated?'<p class="hint">'+esc(T('ev.truncated'))+'</p>':'')+
          (x.run_id?'<a class="btn sm" data-run-log href="#/runs/'+enc(x.run_id)+'">'+esc(T('wf.open_log'))+'</a>':'')+'</section>';
      }
      function poll(gen){if(!selected||gen!==generation||!document.contains(panel))return;
        api('/api/skill-set-evals/'+enc(selected)).then(function(r){
          if(gen!==generation||!document.contains(panel))return;var box=$('#ev-result',panel),active=r.status==='running';
          box.innerHTML='<div class="row-actions"><span class="pill">'+esc(T('ev.status_'+r.status))+'</span><span>'+esc(money(r.cost_usd||0,'USD',4))+' / '+esc(money(r.budget_usd,'USD',2))+'</span>'+(active?'<button class="btn sm" data-cancel>'+esc(T('btn.stop'))+'</button>':'')+'</div><p class="hint"><code>'+esc(r.version_id)+'</code> · '+esc(r.provider)+(r.model?' / '+esc(r.model):'')+'</p>'+
            (r.error?'<p class="notice bad">'+esc(errInfoText(r.error_info,r.error))+'</p>':'')+(r.cases||[]).map(function(c){return '<article class="ss-details"><h3>'+esc(c.name)+'</h3><details><summary>'+esc(T('wf.input'))+'</summary><pre class="wf-output">'+esc(c.input)+'</pre></details><div class="ss-eval-pair">'+outcome(c.baseline,'ev.baseline')+outcome(c.composition,'ev.composition')+'</div></article>';}).join('');
          var cancel=$('[data-cancel]',box);if(cancel)cancel.onclick=function(){cancel.disabled=true;api('/api/skill-set-evals/'+enc(r.id)+'/cancel',{method:'POST',body:{}}).then(function(){poll(++generation);}).catch(function(e){cancel.disabled=false;fail(e);});};
          $$('[data-run-log]',box).forEach(function(a){a.onclick=closeDrawer;});
          if(active)setTimeout(function(){poll(gen);},1500);else history();
        }).catch(function(e){if(document.contains(panel))$('#ev-error',panel).textContent=e.message;});
      }
      history();
    }).catch(fail);
  }
  function renderSkillSets() {
    return Promise.all([api("/api/skill-sets"), api("/api/skills")]).then(function (r) {
      var sets = r[0], skills = r[1];
      mount('<div class="page-head"><div><h1>' + esc(T("ss.title")) + '</h1><p>' + esc(T("ss.subtitle")) + '</p></div><div class="actions"><button type="button" class="btn" id="ss-import-open">' + ic("upload", "sm") + esc(T("ss.import")) + '</button><input type="file" id="ss-import" class="hidden" accept=".zip"><button class="btn primary" id="ss-new">' + ic("plus", "sm") + esc(T("ss.new")) + '</button></div></div>' + skillTabs("skill-sets") +
        (sets.length ? '<div class="ss-grid">' + sets.map(function (set) { return '<section class="card" data-set-card="' + esc(set.id) + '"><div class="card-head"><span class="ico">' + ic("skills") + '</span><h2>' + esc(set.name) + '</h2><span class="tag perm">v' + esc(set.version_no) + '</span></div>' + (set.details && set.details.description ? '<p class="ss-description">' + esc(set.details.description) + '</p>' : '') + '<ul class="list-plain">' + set.skill_bindings.map(function (b) { return '<li><strong>' + esc(T(b.role === "primary" ? "ss.primary" : "ss.auxiliary")) + '</strong> · ' + esc(b.skill_name || b.skill_id) + '</li>'; }).join("") + '</ul><div class="row-actions ss-card-actions"><button class="btn primary sm" data-set-run>' + ic("play", "sm") + esc(T("btn.run")) + '</button><button class="btn sm" data-set-edit>' + esc(T("ss.edit")) + '</button><button class="btn sm" data-set-usage>' + esc(T("ss.usage")) + '</button><button class="btn sm" data-set-eval>' + esc(T("ss.evaluate")) + '</button><button class="btn sm" data-set-check>' + esc(T("ss.dependencies")) + '</button><button class="icon-btn sm" data-set-export aria-label="' + esc(T("ss.export")) + '">' + ic("download", "sm") + '</button><button class="icon-btn sm" data-set-delete aria-label="' + esc(T("ss.delete")) + '">' + ic("trash", "sm") + '</button></div></section>'; }).join("") + '</div>' : emptyState("skills", T("ss.empty"), T("ss.subtitle"))), "skills", [[T("ss.title")]]);
      $('#ss-new').addEventListener("click", function () { skillSetModal(null, skills); });
      var importInput = $('#ss-import'), importButton = $('#ss-import-open');
      importButton.onclick = function () { importInput.click(); };
      $('#ss-import').addEventListener("change", function (e) { var f = e.target.files[0]; if (!f) return; var fd = new FormData(); fd.append("file", f); e.target.disabled = true; importButton.disabled = true; upload("/api/skill-sets/import", fd).then(function () { toast(T("ss.imported")); renderSkillSets(); }).catch(function (err) { e.target.disabled = false; importButton.disabled = false; e.target.value = ""; if(err.data&&err.data.partial)toast(T("ss.import_partial",{n:(err.data.imported_skill_ids||[]).length})+" "+err.message,true);else fail(err); }); });
      $$('[data-set-card]').forEach(function (card) { var set = sets.filter(function (s) { return s.id === card.getAttribute("data-set-card"); })[0];
        $('[data-set-run]', card).addEventListener("click", function () { quickRunModal({ skill_bindings: set.skill_bindings, skill_set_source: {set_id:set.id,version_id:set.version_id} }); });
        $('[data-set-usage]', card).onclick=function(){skillSetUsage(set);};
        $('[data-set-eval]', card).onclick=function(){skillSetEvaluation(set);};
        $('[data-set-edit]', card).addEventListener("click", function () { skillSetModal(set, skills); });
        $('[data-set-check]', card).addEventListener("click", function () { dependencyCheck({ kind: "skill_set", id: set.id }); });
        $('[data-set-export]', card).addEventListener("click", function (e) { downloadFile("/api/skill-sets/" + enc(set.id) + "/export", "tidebot-skill-set.zip", e.currentTarget); });
        $('[data-set-delete]', card).addEventListener("click", function () { confirmDialog({title:T("ss.delete"),body:T("ss.delete_note",{name:set.name}),ok:T("ss.delete"),danger:true}).then(function(yes){if(yes)api("/api/skill-sets/"+enc(set.id),{method:"DELETE"}).then(renderSkillSets).catch(fail);}); });
      });
    });
  }

  var workflowDraftGuard = null;
  // ---------- workflow canvas ----------
  function wfStatus(s) { return '<span class="pill ' + (s==='succeeded'?'ok':s==='failed'||s==='interrupted'?'bad':s==='running'||s==='queued'?'info':'') + '">' + esc(T('wf.status_'+s)) + '</span>'; }
  function wfLevels(nodes) {
    var levels=Object.create(null), pending=nodes.slice(), count=0;
    while(pending.length && count++<=nodes.length) {
      var next=[];pending.forEach(function(n){if((n.needs||[]).every(function(d){return levels[d.node_id]!=null;}))levels[n.id]=(n.needs||[]).reduce(function(m,d){return Math.max(m,levels[d.node_id]+1);},0);else next.push(n);});
      if(next.length===pending.length)return null;pending=next;
    } return levels;
  }
  function wfArrange(def) {
    var levels=wfLevels(def.nodes);if(!levels)return false;var rows={};
    def.nodes.forEach(function(n){var l=levels[n.id];n.position={x:40+l*290,y:50+(rows[l]||0)*150};rows[l]=(rows[l]||0)+1;});return true;
  }
  function wfScene(def, selected, states) {
    var nodes=def.nodes||[], points=Object.create(null), width=1000,height=600,lines=[],boxes=[];
    nodes.forEach(function(n,i){var p=n.position||{x:40+(i%3)*290,y:50+Math.floor(i/3)*150};points[n.id]=p;width=Math.max(width,p.x+280);height=Math.max(height,p.y+150);});
    nodes.forEach(function(n){var p=points[n.id];(n.needs||[]).forEach(function(d){var q=points[d.node_id];if(!q)return;var y=q.y+(d.branch==='true'?30:d.branch==='false'?65:43);lines.push('<path class="wf-edge" d="M '+(q.x+200)+' '+y+' C '+(q.x+250)+' '+y+' '+(p.x-50)+' '+(p.y+43)+' '+p.x+' '+(p.y+43)+'"/>');if(d.branch)lines.push('<text class="wf-edge-label" x="'+(q.x+214)+'" y="'+(y-10)+'">'+esc(T('wf.branch_'+d.branch))+'</text>');});
      var status=states&&states[n.id],title='',units=0;Array.from(n.name||n.id).some(function(c){var size=c.charCodeAt(0)>127?2:1;if(units+size>22){title+='…';return true;}title+=c;units+=size;return false;});
      boxes.push('<g class="wf-node '+(n.kind==='condition'?'condition ':'')+(selected===n.id?'selected ':'')+esc(status||'')+'" data-node="'+esc(n.id)+'" tabindex="0" role="button" aria-label="'+esc(n.name||n.id)+'" transform="translate('+p.x+' '+p.y+')"><rect class="wf-node-bg" width="200" height="86" rx="14"/><text x="16" y="27" class="wf-node-title">'+esc(title)+'</text><text x="16" y="49" class="wf-node-sub">'+esc(n.id)+'</text><text x="16" y="70" class="wf-node-sub">'+esc(status?T('wf.status_'+status):T(n.kind==='condition'?'wf.condition':'wf.skill_step'))+'</text><circle class="wf-port in" data-in="'+esc(n.id)+'" cx="0" cy="43" r="8"/>'+(n.kind==='condition'?'<circle class="wf-port true" data-out="'+esc(n.id)+'" data-branch="true" cx="200" cy="30" r="8"/><circle class="wf-port false" data-out="'+esc(n.id)+'" data-branch="false" cx="200" cy="65" r="8"/>':'<circle class="wf-port out" data-out="'+esc(n.id)+'" data-branch="" cx="200" cy="43" r="8"/>')+'</g>');
    });return {html:lines.join('')+boxes.join(''),width:width,height:height};
  }
  function wfDiagram(def, results) {
    var states=Object.create(null);(results||[]).forEach(function(n){states[n.node_id]=n.status;});var copy=JSON.parse(JSON.stringify(def));if(copy.nodes.some(function(n){return !n.position;}))wfArrange(copy);var scene=wfScene(copy,'',states);
    return '<div class="wf-trace-graph"><svg role="img" aria-label="'+esc(T('wf.graph'))+'" viewBox="0 0 '+scene.width+' '+scene.height+'">'+scene.html+'</svg></div>';
  }
  // P4: a workflow's folder and permission level (every node inherits them), and what
  // runs it on a schedule (tasks whose target is the workflow).
  function wfMode(d) { return (d && d.run_mode) || 'read'; }
  function wfModeNotice(d) {
    var m = wfMode(d), dir = d.workspace_dir || '';
    return T('wf.run_notice_' + m + (dir ? '' : '_scratch'), { p: dir }) + (m !== 'read' ? ' ' + T('wf.serial_note') : '') + (d.budget > 0 ? ' ' + T('wf.budget_note', { b: money(d.budget, d.budget_currency) }) : '');
  }
  function wfTasksPill(w) {
    var ts = w.tasks || [];
    if (!ts.length) return '';
    var lines = ts.map(function (t) { return t.name + ' · ' + (t.cron ? cronHuman(t.cron) : T('se.manual')) + (t.status === 'paused' ? ' · ' + T('wf.task_paused') : ''); }).join('\n');
    var first = ts[0];
    return '<a class="pill plain sm wf-sched-pill" href="#/tasks/' + enc(first.id) + '" data-tip="' + esc(T('wf.tasks_tip', { l: lines })) + '">' + ic('clock', 'sm') + esc(ts.length > 1 ? T('wf.tasks_n', { n: ts.length }) : (first.cron ? cronHuman(first.cron) : T('se.manual'))) + '</a>';
  }
  function wfSchedule(w) {
    // the task form, its target the workflow pinned to the version shown (manual until a schedule is set)
    taskFormModal(null, { name: w.name, workflow: { id: w.id, version: w.version, current: w.version, name: w.name } });
  }
  function renderWorkflows() {
    return api('/api/workflows').then(function(items){
      mount('<div class="page-head wf-page-head"><div><h1>'+esc(T('nav.workflows'))+'</h1><p>'+esc(T('wf.subtitle'))+'</p></div><div class="actions"><a class="btn primary" href="#/workflows/new">'+ic('plus','sm')+esc(T('wf.create'))+'</a></div></div>'+skillTabs('workflows')+
        (items.length?'<div class="wf-library">'+items.map(function(w){return '<article class="card"><div class="row-actions"><span class="ico">'+ic('branch')+'</span><h2><a href="#/workflows/'+enc(w.id)+'">'+esc(w.name)+'</a></h2><span class="grow"></span>'+wfTasksPill(w)+'<span class="tag" data-tip="'+esc(wfModeNotice(w.definition))+'">'+esc(T('rn.'+wfMode(w.definition)))+'</span><span class="tag">v'+esc(w.version)+'</span></div><p class="muted">'+esc(w.description||T('wf.subtitle'))+'</p><p>'+esc(T('wf.count',{n:w.definition.nodes.length,p:w.definition.max_parallel}))+'</p><div class="row-actions"><a class="btn" href="#/workflows/'+enc(w.id)+'">'+esc(T('btn.edit'))+'</a><button class="btn" data-wf-deps="'+esc(w.id)+'">'+ic('shield','sm')+esc(T('dep.check'))+'</button><button class="btn primary" data-wf-run="'+esc(w.id)+'">'+ic('play','sm')+esc(T('btn.run'))+'</button></div></article>';}).join('')+'</div>':emptyState('branch',T('wf.none'),T('wf.none_hint'))),'workflows',[[T('nav.workflows')]]);
      $$('[data-wf-run]').forEach(function(b){b.onclick=function(){workflowRunModal(b.dataset.wfRun);};});
      $$('[data-wf-deps]').forEach(function(b){b.onclick=function(){dependencyCheck({kind:'workflow',id:b.dataset.wfDeps});};});
    });
  }
  function workflowRunModal(id) {
    Promise.all([api('/api/workflows/'+enc(id)),api('/api/settings')]).then(function(r){var w=r[0],st=r[1];
      modal('<div class="card-head"><h2>'+esc(w.name)+'</h2><span class="tag">v'+esc(w.version)+'</span></div><form id="wf-run-form">'+fieldHtml(T('wf.input'),'<textarea name="input" rows="5" maxlength="32000" placeholder="'+esc(T('wf.input_hint'))+'"></textarea>')+fieldHtml(T('f.provider'),'<select name="provider">'+providerOptions(st, smartProvider(st), false, false)+'</select>')+'<p class="notice">'+esc(wfModeNotice(w.definition))+'</p><p class="field-err" id="wf-run-error"></p><div class="actions"><button type="button" class="btn ghost" data-close>'+esc(T('btn.cancel'))+'</button><button type="submit" class="btn primary">'+ic('play','sm')+esc(T('btn.run'))+'</button></div></form>',function(root,close){
        var f=$('#wf-run-form',root);f.onsubmit=function(e){e.preventDefault();if(!readyProviders(st).some(function(p){return p.name===f.provider.value;})){$('#wf-run-error',root).textContent=T('cc.no_provider');return;}var b=f.querySelector('[type=submit]');b.disabled=true;
          (wfMode(w.definition)==='exec'?execFirstUse(st.sandbox,f):Promise.resolve(true)).then(function(ok){if(!ok){b.disabled=false;return;}
          api('/api/workflows/'+enc(id)+'/run',{method:'POST',body:{input:f.input.value,provider:f.provider.value}}).then(function(x){close();location.hash='#/workflow-runs/'+enc(x.run_id);}).catch(function(e){b.disabled=false;var pr=e.data&&e.data.problem;$('#wf-run-error',root).textContent=pr?T('rn.ws_'+pr+'_tip'):e.message;});});};
      });
    }).catch(fail);
  }
  function renderWorkflowEditor(id) {
    var routeKey=location.hash,creating=!id||id==='new';
    return Promise.all([api('/api/skills'),api('/api/skill-sets'),creating?Promise.resolve(null):api('/api/workflows/'+enc(id)),api('/api/settings').catch(function(){return {};})]).then(function(r){
      if(location.hash!==routeKey || workflowDraftGuard&&workflowDraftGuard.dirty())return;
      var skills=r[0],sets=r[1],saved=r[2],def=saved?JSON.parse(JSON.stringify(saved.definition)):{name:'',description:'',max_parallel:2,timeout_minutes:0,run_mode:'exec',workspace_dir:'',nodes:[]},st=r[3]||{},cur=(st.fx||{}).display||state.currency||'USD';
      // saved before the three levels: read-only, as it always ran (keeps its values)
      if(!def.run_mode)def.run_mode='read';if(!def.workspace_dir)def.workspace_dir='';if(!def.budget){def.budget=0;delete def.budget_currency;}
      if(def.nodes.some(function(n){return !n.position;}))wfArrange(def);
      var selected=def.nodes.length?def.nodes[0].id:'',zoom=0.85,drag=null,link=null,baseline=JSON.stringify(def);
      mount('<div class="page-head wf-page-head"><div><a href="#/workflows">← '+esc(T('nav.workflows'))+'</a><h1>'+esc(creating?T('wf.create'):saved.name)+'</h1></div><div class="row-actions actions"><button class="btn" id="wf-check">'+ic('shield','sm')+esc(T('dep.check'))+'</button><button class="btn primary" id="wf-save">'+ic('check','sm')+esc(T('btn.save'))+'</button>'+(creating?'':'<button class="btn" id="wf-sched" data-tip="'+esc(T('wf.sched_tip'))+'">'+ic('clock','sm')+esc(T('wf.sched'))+'</button><button class="btn" id="wf-run">'+ic('play','sm')+esc(T('btn.run'))+'</button>')+'</div></div>'+
        '<form id="wf-meta" class="card wf-meta"><div class="wf-meta-name">'+fieldHtml(T('tf.name'),'<input name="name" required maxlength="80" value="'+esc(def.name)+'">',null,saved?wfTasksPill(saved):'')+'</div>'+
        '<div class="field ws-field wf-ws"><span class="rn-ws-lbl"><span id="wf-wlbl">'+esc(T('rn.folder'))+'</span>'+tip(T('wf.folder_tip'))+'<span class="pill sm hidden" id="wf-ws-st"></span><span class="spacer"></span>'+modeSegHtml('run_mode',def.run_mode,st.sandbox)+'</span>'+
        '<div class="in-group ws-in"><input name="workspace_dir" class="mono" spellcheck="false" autocomplete="off" autocapitalize="off" maxlength="1024" aria-labelledby="wf-wlbl" placeholder="'+esc(T('rn.folder_ph'))+'" value="'+esc(def.workspace_dir)+'"><button type="button" class="icon-btn sm hidden" id="wf-ws-clear" data-tip="'+esc(T('rn.folder_clear'))+'" aria-label="'+esc(T('rn.folder_clear'))+'">'+ic('x','sm')+'</button></div><p class="hint" id="wf-ws-hint" data-tip=""></p></div><details class="wf-options"><summary>'+esc(T('wf.options'))+'</summary><div class="form-row">'+fieldHtml(T('wf.parallel'),'<input name="max_parallel" type="number" min="1" max="4" value="'+def.max_parallel+'">')+'<div class="field wf-timeout"><label class="check"><input type="checkbox" name="timeout_enabled"'+(def.timeout_minutes>0?' checked':'')+'> '+esc(T('wf.timeout_enabled'))+tip(T('wf.timeout_tip'))+'</label><input name="timeout_minutes" type="number" min="1" max="1440" aria-label="'+esc(T('tf.timeout'))+'" placeholder="'+esc(T('tf.no_cap'))+'" value="'+(def.timeout_minutes>0?def.timeout_minutes:'')+'"'+(def.timeout_minutes>0?'':' disabled')+'></div><div class="field wf-timeout wf-budget"><label class="check"><input type="checkbox" name="budget_enabled"'+(def.budget>0?' checked':'')+'> '+esc(T('wf.budget_enabled'))+tip(T('wf.budget_tip'))+'</label><input name="budget" type="number" min="0.0001" max="10000" step="0.0001" aria-label="'+esc(T('wf.budget_c',{c:def.budget_currency||cur}))+'" placeholder="'+esc(T('wf.budget_c',{c:def.budget_currency||cur}))+'" value="'+(def.budget>0?def.budget:'')+'"'+(def.budget>0?'':' disabled')+'></div></div>'+fieldHtml(T('sk.desc'),'<input name="description" maxlength="4096" value="'+esc(def.description||'')+'">')+'</details></form>'+
        '<div class="wf-designer"><aside class="card wf-palette"><h2>'+esc(T('wf.palette'))+'</h2><p class="hint">'+esc(T('wf.drag_hint'))+'</p><div id="wf-palette-skills">'+skills.map(function(s,i){return '<button type="button" class="wf-palette-item" draggable="true" data-palette="skill" data-palette-index="'+i+'">'+ic('skills','sm')+'<span>'+esc(s.name)+'</span></button>';}).join('')+(skills.length?'':'<a href="#/skills">'+esc(T('wf.need_skill'))+'</a>')+'</div><h3>'+esc(T('ss.title'))+'</h3>'+sets.map(function(s,i){return '<button type="button" class="wf-palette-item" draggable="true" data-palette="set" data-palette-index="'+i+'">'+ic('layers','sm')+'<span>'+esc(s.name)+'</span></button>';}).join('')+'<h3>'+esc(T('wf.control'))+'</h3><button type="button" class="wf-palette-item" draggable="true" data-palette="condition">'+ic('branch','sm')+esc(T('wf.condition'))+'</button></aside>'+
        '<section class="card wf-canvas-panel"><div class="wf-canvas-toolbar"><button class="btn sm" id="wf-arrange">'+esc(T('wf.arrange'))+'</button><button class="btn sm" id="wf-example">'+esc(T('wf.example'))+'</button><span class="grow"></span><label>'+esc(T('wf.zoom'))+' <select id="wf-zoom">'+[50,75,85,100,125].map(function(n){return opt(n,n+'%',n===85);}).join('')+'</select></label></div><p class="hint" id="wf-canvas-hint">'+esc(T('wf.connect_hint'))+'</p><div class="wf-canvas-scroll" id="wf-canvas-scroll"><svg id="wf-canvas" role="group" aria-label="'+esc(T('wf.graph'))+'" tabindex="0"></svg></div></section>'+
        '<aside class="card wf-inspector" id="wf-inspector"></aside></div><p class="notice bad hidden" id="wf-error" role="alert"></p>'+
        (creating?'':'<section class="card"><div class="card-head"><h2>'+esc(T('wf.history'))+'</h2><span class="grow"></span><button class="btn ghost sm" id="wf-delete">'+esc(T('btn.delete'))+'</button></div><div id="wf-history"></div></section>'),'workflows',[[T('nav.workflows'),'#/workflows'],[creating?T('wf.create'):saved.name]]);
      var f=$('#wf-meta'),svg=$('#wf-canvas'),inspector=$('#wf-inspector'),host=$('#wf-canvas-scroll');
      function read(){def.name=f.name.value.trim();def.description=f.description.value;def.max_parallel=+f.max_parallel.value;def.timeout_minutes=f.timeout_enabled.checked?+f.timeout_minutes.value||0:0;var mc=f.querySelector('input[name="run_mode"]:checked');def.run_mode=mc?mc.value:'read';def.workspace_dir=f.workspace_dir.value.trim();var bv=f.budget_enabled.checked?+f.budget.value||0:0;if(bv>0){def.budget=bv;def.budget_currency=def.budget_currency||cur;}else{def.budget=0;delete def.budget_currency;}return def;}
      f.budget_enabled.onchange=function(){f.budget.disabled=!this.checked;if(this.checked)f.budget.focus();};
      // folder + permission: like the Run dialog (可修改 needs a folder); the hint says what nodes may do
      var wsPill=$('#wf-ws-st'),wsClear=$('#wf-ws-clear'),wsHint=$('#wf-ws-hint'),wsSeq=0,wsTimer=0;
      function paintMode(){var dir=f.workspace_dir.value.trim();$$('input[name="run_mode"]',f).forEach(function(i){i.disabled=!dir&&i.value==='edit';});var c=f.querySelector('input[name="run_mode"]:checked');if(!dir&&c&&c.value==='edit'){f.querySelector('input[name="run_mode"][value="exec"]').checked=true;}var d=read();wsHint.textContent=wfModeNotice(d);wsHint.setAttribute('data-tip',wsHint.textContent);wsClear.classList.toggle('hidden',!dir);}
      function checkWs(){var my=++wsSeq,v=f.workspace_dir.value.trim();paintMode();if(!v){wsPill.className='pill sm hidden';return;}api('/api/tasks/workspace-check?mode=run&path='+enc(v)).then(function(x){if(my!==wsSeq)return;x=x||{};var pr=x.problem||'';wsPill.className='pill sm '+(pr?'bad':'ok');wsPill.textContent=pr?T('rn.ws_'+pr):T('rn.ws_ok');wsPill.setAttribute('data-tip',pr?T('rn.ws_'+pr+'_tip'):(x.path||''));}).catch(function(){});}
      f.workspace_dir.addEventListener('input',function(){clearTimeout(wsTimer);wsTimer=setTimeout(checkWs,250);paintMode();});
      wsClear.onclick=function(){f.workspace_dir.value='';checkWs();f.workspace_dir.focus();};
      $$('input[name="run_mode"]',f).forEach(function(i){i.addEventListener('change',paintMode);});
      f.timeout_enabled.onchange=function(){f.timeout_minutes.disabled=!this.checked;if(this.checked&&!f.timeout_minutes.value)f.timeout_minutes.value=60;if(this.checked)f.timeout_minutes.focus();};
      function error(msg){var el=$('#wf-error');el.textContent=msg||'';el.classList.toggle('hidden',!msg);}
      function point(e){var p=svg.createSVGPoint();p.x=e.clientX;p.y=e.clientY;return p.matrixTransform(svg.getScreenCTM().inverse());}
      function canvas(){var scene=wfScene(def,selected);svg.setAttribute('viewBox','0 0 '+scene.width+' '+scene.height);svg.style.width=(scene.width*zoom)+'px';svg.style.height=(scene.height*zoom)+'px';svg.innerHTML=scene.html;if(link&&link.to){var p=def.nodes.find(function(n){return n.id===link.from;}).position,y=p.y+(link.branch==='true'?30:link.branch==='false'?65:43);svg.insertAdjacentHTML('beforeend','<path class="wf-link-draft" d="M '+(p.x+200)+' '+y+' L '+link.to.x+' '+link.to.y+'"/>');}}
      function connect(from,to,branch){if(from===to)return;var target=def.nodes.find(function(n){return n.id===to;});if(!target)return;var before=(target.needs||[]).slice();target.needs=before.filter(function(d){return d.node_id!==from;});target.needs.push({node_id:from,branch:branch||''});if(!wfLevels(def.nodes)){target.needs=before;error(T('wf.cycle'));}else error('');selected=to;canvas();inspect();}
      function inspect(){
        var n=def.nodes.find(function(n){return n.id===selected;});if(!n){inspector.innerHTML='<h2>'+esc(T('wf.properties'))+'</h2><p class="muted">'+esc(T('wf.select_hint'))+'</p>';return;}
        inspector.innerHTML='<div class="card-head"><h2>'+esc(T('wf.properties'))+'</h2><span class="grow"></span><button class="icon-btn" id="wf-remove" aria-label="'+esc(T('btn.remove'))+'">'+ic('trash','sm')+'</button></div><p><code>'+esc(n.id)+'</code> · '+esc(T(n.kind==='condition'?'wf.condition':'wf.skill_step'))+'</p>'+fieldHtml(T('tf.name'),'<input id="wf-node-name" maxlength="80" value="'+esc(n.name)+'">')+
          '<div class="wf-needs"><strong>'+esc(T('wf.predecessors'))+'</strong>'+def.nodes.filter(function(p){return p.id!==n.id;}).map(function(p){var edge=(n.needs||[]).find(function(d){return d.node_id===p.id;});return '<label class="wf-need-row"><span>'+esc(p.name||p.id)+'</span><select data-wf-need="'+esc(p.id)+'">'+opt('',T('wf.no_dependency'),!edge)+(p.kind==='condition'?opt('true',T('wf.branch_true'),edge&&edge.branch==='true')+opt('false',T('wf.branch_false'),edge&&edge.branch==='false'):opt('success',T('wf.after_success'),!!edge))+'</select></label>';}).join('')+'</div>'+fieldHtml(T('wf.join'),'<select id="wf-node-join">'+opt('all',T('wf.join_all'),n.join!=='any')+opt('any',T('wf.join_any'),n.join==='any')+'</select>')+
          (n.kind==='skill'?'<div id="wf-bindings"></div>'+fieldHtml(T('wf.retry_max'),'<input id="wf-retry-max" type="number" min="0" max="5" step="1" value="'+esc(n.retry_max||0)+'">')+fieldHtml(T('wf.retry_backoff'),'<input id="wf-retry-backoff" type="number" min="1" max="60000" step="1" value="'+esc(n.retry_backoff_ms||1000)+'">')+'<p class="hint">'+esc(T('wf.retry_hint'))+'</p>'+workflowContextFields(n)+fieldHtml(T('tf.prompt'),'<textarea id="wf-node-prompt" rows="7" maxlength="32000">'+esc(n.prompt||'')+'</textarea>')+'<label class="field"><span>'+esc(T('wf.insert_output'))+'</span><select id="wf-insert-output"><option value="">'+esc(T('wf.choose_output'))+'</option><option value="{{input}}">'+esc(T('wf.input'))+'</option>'+def.nodes.filter(function(p){return p.id!==n.id&&p.kind==='skill';}).map(function(p){return opt('{{steps.'+p.id+'.output}}',p.name+' · '+p.id);}).join('')+'</select></label><p class="hint">'+esc(T('wf.input_template_hint'))+'</p>':
          fieldHtml(T('wf.condition_source'),'<input id="wf-condition-source" value="'+esc(n.condition.source)+'">')+fieldHtml(T('wf.operator'),'<select id="wf-condition-operator">'+['contains','not_contains','equals','not_equals','empty','not_empty'].map(function(o){return opt(o,T('wf.op_'+o),o===n.condition.operator);}).join('')+'</select>')+fieldHtml(T('wf.compare_value'),'<input id="wf-condition-value" value="'+esc(n.condition.value||'')+'">')+'<p class="hint">'+esc(T('wf.condition_hint'))+'</p>');
        $('#wf-node-name').oninput=function(){n.name=this.value;canvas();};$('#wf-node-join').onchange=function(){n.join=this.value;};
        $('#wf-remove').onclick=function(){if(link&&link.from===n.id)link=null;if(drag&&drag.id===n.id)drag=null;def.nodes=def.nodes.filter(function(p){return p.id!==n.id;});def.nodes.forEach(function(p){p.needs=(p.needs||[]).filter(function(d){return d.node_id!==n.id;});});selected='';canvas();inspect();};
        $$('[data-wf-need]',inspector).forEach(function(el){el.onchange=function(){if(el.value)connect(el.dataset.wfNeed,n.id,el.value==='success'?'':el.value);else{n.needs=(n.needs||[]).filter(function(d){return d.node_id!==el.dataset.wfNeed;});canvas();}};});
        if(n.kind==='skill'){
          $('#wf-context-enabled').onchange=function(){if(this.checked)n.context={mode:'full',max_tokens:2048,budget:{max_input_tokens:65536}};else delete n.context;inspect();};
          if(n.context){$('#wf-context-budget').oninput=function(){n.context.budget=n.context.budget||{};n.context.budget.max_input_tokens=+this.value;};$('#wf-context-mode').onchange=function(){n.context.mode=this.value;};$('#wf-context-limit').oninput=function(){n.context.max_tokens=+this.value;};}
          $('#wf-retry-max').oninput=function(){n.retry_max=+this.value;};
          $('#wf-retry-backoff').oninput=function(){n.retry_backoff_ms=+this.value;};
          skillBindingEditor($('#wf-bindings'),skills,n.skill_bindings||[],function(bs,source){n.skill_bindings=bs;n.skill_set_source=source;},{source:n.skill_set_source});
          var ta=$('#wf-node-prompt');ta.oninput=function(){n.prompt=ta.value;};
          $('#wf-insert-output').onchange=function(){if(!this.value)return;ta.setRangeText(this.value,ta.selectionStart,ta.selectionEnd,'end');n.prompt=ta.value;ta.focus();this.value='';};
        }else ['source','operator','value'].forEach(function(k){$('#wf-condition-'+k).oninput=function(){n.condition[k]=this.value;};});
      }
      function add(kind,index,position){
        if(def.nodes.length>=32){error(T('wf.limit'));return;}var i=1;while(def.nodes.some(function(n){return n.id==='step_'+i;}))i++;
        var item=kind==='set'?sets[index]:kind==='skill'?skills[index]:null,n={id:'step_'+i,name:item?item.name:T('wf.condition'),kind:kind==='condition'?'condition':'skill',needs:[],join:'all',position:position||{x:40+(def.nodes.length%3)*290,y:50+Math.floor(def.nodes.length/3)*150}};
        if(n.kind==='condition')n.condition={source:'{{input}}',operator:'contains',value:''};else {n.prompt='{{input}}';n.skill_bindings=kind==='set'?JSON.parse(JSON.stringify(item.skill_bindings)):[{skill_id:item.id,role:'primary',mode:'follow'}];n.skill_set_source=kind==='set'?{set_id:item.id,version_id:item.version_id}:null;}
        def.nodes.push(n);selected=n.id;canvas();inspect();
      }
      $$('[data-palette]').forEach(function(el){el.ondragstart=function(e){e.dataTransfer.setData('application/x-tidebot-step',JSON.stringify({kind:el.dataset.palette,index:+el.dataset.paletteIndex||0}));e.dataTransfer.effectAllowed='copy';};el.onclick=function(){add(el.dataset.palette,+el.dataset.paletteIndex||0);};});
      host.ondragover=function(e){if(Array.from(e.dataTransfer.types).indexOf('application/x-tidebot-step')>=0){e.preventDefault();e.dataTransfer.dropEffect='copy';}};
      host.ondrop=function(e){e.preventDefault();try{var data=JSON.parse(e.dataTransfer.getData('application/x-tidebot-step')),p=point(e);if(['skill','set','condition'].indexOf(data.kind)<0)return;if(data.kind==='skill'&&!skills[data.index]||data.kind==='set'&&!sets[data.index])return;add(data.kind,data.index,{x:Math.max(0,Math.min(10000,p.x-100)),y:Math.max(0,Math.min(10000,p.y-43))});}catch(ex){error(T('wf.bad_drop'));}};
      svg.onpointerdown=function(e){
        if(e.button!==0)return;var out=e.target.closest('[data-out]'),input=e.target.closest('[data-in]'),node=e.target.closest('[data-node]');
        if(input&&link){connect(link.from,input.dataset.in,link.branch);link=null;canvas();return;}
        if(out){link={from:out.dataset.out,branch:out.dataset.branch,to:point(e)};svg.setPointerCapture(e.pointerId);e.preventDefault();return;}
        if(node){selected=node.dataset.node;var n=def.nodes.find(function(n){return n.id===selected;}),p=point(e);drag={id:n.id,x:p.x-n.position.x,y:p.y-n.position.y};svg.setPointerCapture(e.pointerId);canvas();inspect();e.preventDefault();}
      };
      svg.onpointermove=function(e){var p=point(e);if(drag){var n=def.nodes.find(function(n){return n.id===drag.id;});n.position={x:Math.max(0,Math.min(10000,p.x-drag.x)),y:Math.max(0,Math.min(10000,p.y-drag.y))};canvas();}else if(link){link.to=p;canvas();}};
      svg.onpointerup=function(e){if(link){var hit=document.elementFromPoint(e.clientX,e.clientY),node=hit&&hit.closest('[data-node]');if(node&&node.dataset.node!==link.from){connect(link.from,node.dataset.node,link.branch);link=null;}}drag=null;if(svg.hasPointerCapture(e.pointerId))svg.releasePointerCapture(e.pointerId);canvas();};
      svg.onpointercancel=function(){drag=null;link=null;canvas();};
      svg.onkeydown=function(e){var el=e.target.closest('[data-node]');if(e.key==='Escape'){link=null;canvas();return;}if(!el)return;selected=el.dataset.node;var n=def.nodes.find(function(n){return n.id===selected;});if(e.key==='Enter'||e.key===' '){e.preventDefault();inspect();return;}var delta={ArrowLeft:[-10,0],ArrowRight:[10,0],ArrowUp:[0,-10],ArrowDown:[0,10]}[e.key];if(delta){e.preventDefault();n.position.x=Math.max(0,Math.min(10000,n.position.x+delta[0]));n.position.y=Math.max(0,Math.min(10000,n.position.y+delta[1]));canvas();var focus=$$('[data-node]',svg).find(function(el){return el.dataset.node===selected;});if(focus)focus.focus();}};
      $('#wf-arrange').onclick=function(){if(!wfArrange(def))error(T('wf.cycle'));canvas();};
      $('#wf-zoom').onchange=function(){zoom=+this.value/100;canvas();};
      $('#wf-example').onclick=function(){
        if(def.nodes.length){error(T('wf.example_empty'));return;}
        var binding=[],step=function(id,name,prompt,needs,join){return {id:id,name:T(name),kind:'skill',prompt:prompt,needs:needs,join:join||'all',skill_bindings:JSON.parse(JSON.stringify(binding))};};
        def.nodes=[step('prepare','wf.sample_prepare','{{input}}',[]),{id:'gate',name:T('wf.sample_gate'),kind:'condition',needs:[{node_id:'prepare'}],join:'all',condition:{source:'{{steps.prepare.output}}',operator:'not_empty',value:''}},step('research','wf.sample_research',T('wf.sample_research_prompt')+'\n{{steps.prepare.output}}',[{node_id:'gate',branch:'true'}]),step('review','wf.sample_review',T('wf.sample_review_prompt')+'\n{{steps.prepare.output}}',[{node_id:'gate',branch:'true'}]),step('fallback','wf.sample_fallback','{{input}}',[{node_id:'gate',branch:'false'}]),step('merge','wf.sample_merge',T('wf.sample_merge_prompt')+'\n{{steps.research.output}}\n{{steps.review.output}}\n{{steps.fallback.output}}',[{node_id:'research'},{node_id:'review'},{node_id:'fallback'}],'any')];wfArrange(def);selected='prepare';canvas();inspect();toast(T('wf.example_choose'));
      };
      $('#wf-check').onclick=function(){dependencyCheck({kind:'workflow_draft',definition:JSON.parse(JSON.stringify(read()))});};
      function save(e){if(e)e.preventDefault();if(!f.reportValidity())return;var b=$('#wf-save'),value=JSON.parse(JSON.stringify(read())),wasCreating=creating;b.disabled=true;error('');api(creating?'/api/workflows':'/api/workflows/'+enc(id),{method:creating?'POST':'PUT',body:creating?value:{expected_version:saved.version,definition:value}}).then(function(w){if(!document.contains(f))return;saved=w;baseline=JSON.stringify(value);toast(T('wf.saved'));if(wasCreating){creating=false;id=w.id;if(JSON.stringify(read())===baseline)location.hash='#/workflows/'+enc(w.id);else{history.replaceState(null,'','#/workflows/'+enc(w.id));lastHash=location.hash;}}else if(!$('#wf-run')&&JSON.stringify(read())===baseline){refresh();}}).catch(function(e){if(document.contains(f)){var pr=e.data&&e.data.problem;error(pr?T('rn.ws_'+pr+'_tip'):e.message);}}).finally(function(){b.disabled=false;});}
      f.onsubmit=save;$('#wf-save').onclick=save;
      if(!creating){$('#wf-sched').onclick=function(){if(JSON.stringify(read())!==baseline){error(T('wf.save_first_sched'));return;}wfSchedule(saved);};$('#wf-run').onclick=function(){if(JSON.stringify(read())!==baseline){error(T('wf.save_first'));return;}workflowRunModal(id);};$('#wf-delete').onclick=function(){confirmDialog({title:T('wf.delete_title'),body:T('wf.delete_hint'),ok:T('btn.delete'),danger:true}).then(function(yes){if(yes)api('/api/workflows/'+enc(id),{method:'DELETE'}).then(function(){location.hash='#/workflows';}).catch(fail);});};api('/api/workflow-runs?workflow_id='+enc(id)).then(function(rs){var h=document.contains(f)?$('#wf-history'):null;if(h)h.innerHTML=rs.length?'<ul class="wf-history">'+rs.map(function(x){return '<li><a href="#/workflow-runs/'+enc(x.id)+'">'+esc(fmtTime(x.created_at))+'</a><span class="tag">v'+esc(x.workflow_version)+'</span>'+wfStatus(x.status)+'</li>';}).join('')+'</ul>':'<p class="muted">'+esc(T('wf.no_runs'))+'</p>';}).catch(fail);}
      baseline=JSON.stringify(read());checkWs();
      workflowDraftGuard={dirty:function(){return document.contains(f)&&JSON.stringify(read())!==baseline;}};
      canvas();inspect();
    });
  }
  // Execution page. A change seen by the poll is painted from that same response (one request,
  // not a second fetch) and keeps what the reader set: which sections are open, how far each
  // output is scrolled, the page scroll. While text is selected or a dialog is open the new data
  // waits for the next quiet check. Polling stops once the run has ended — it used to go on
  // comparing against the stale running snapshot, refetching and repainting every 2 s for as long
  // as the finished page stayed open, reopening every output the reader had folded.
  // (idea: uptime-kuma src/mixins/socket.js patches the monitor list in place instead of rebuilding.)
  function wfRunKeep(box) {
    var k = Object.create(null);
    $$('details[data-wf-d]', box).forEach(function (d) { var pre = $('pre', d); k[d.getAttribute('data-wf-d')] = { open: d.open, top: pre ? pre.scrollTop : 0 }; });
    return k;
  }
  function wfRunRestore(box, k) {
    $$('details[data-wf-d]', box).forEach(function (d) { var s = k[d.getAttribute('data-wf-d')]; if (!s) return; d.open = s.open; var pre = $('pre', d); if (pre && s.top) pre.scrollTop = s.top; });
  }
  function renderWorkflowRun(id) {
    return api('/api/workflow-runs/'+enc(id)).then(function(r){ paintWorkflowRun(id, r, null); });
  }
  function paintWorkflowRun(id, r, keep) {
      var active=r.status==='queued'||r.status==='running',byID=Object.create(null),order=Object.create(null);r.snapshot.nodes.forEach(function(n,i){byID[n.id]=n;order[n.id]=i;});var ordered=r.nodes.slice().sort(function(a,b){return order[a.node_id]-order[b.node_id];});
      mount('<div class="page-head wf-page-head"><div><a href="#/workflows/'+enc(r.workflow_id)+'">← '+esc(r.snapshot.name)+'</a><h1>'+esc(T('wf.execution'))+'</h1></div><div class="row-actions actions">'+wfStatus(r.status)+(active?'<button class="btn danger" id="wf-stop">'+esc(T('btn.stop'))+'</button>':'')+'</div></div><section class="card"><div class="row-actions"><span class="tag">v'+esc(r.workflow_version)+'</span><span class="muted">'+esc(fmtTime(r.created_at))+'</span><span>'+esc(r.provider)+'</span></div><p class="hint">'+esc(T('wf.snapshot_hint'))+'</p><details data-wf-d="input"><summary>'+esc(T('wf.input'))+'</summary><pre class="wf-output">'+esc(r.input)+'</pre></details>'+(r.error?'<p class="notice bad">'+esc(errInfoText(r.error_info,r.error))+'</p>':'')+wfDiagram(r.snapshot,r.nodes)+'</section><div class="wf-execution">'+ordered.map(function(n){var step=byID[n.node_id]||{};return '<article class="card"><div class="card-head"><strong>'+esc(step.name||n.node_id)+'</strong><code>'+esc(n.node_id)+'</code><span class="grow"></span>'+wfStatus(n.status)+'</div>'+(n.child_run_id?'<a class="btn sm" href="#/runs/'+enc(n.child_run_id)+'">'+ic('terminal','sm')+esc(T('wf.open_log'))+'</a>':'')+(n.branch?'<p>'+esc(T('wf.branch_result'))+' '+esc(T('wf.branch_'+n.branch))+'</p>':'')+(n.error?'<p class="notice bad">'+esc(errInfoText(n.error_info,n.error))+'</p>':'')+(n.output?'<details open data-wf-d="out:'+esc(n.node_id)+'"><summary>'+esc(T('wf.output'))+'</summary><pre class="wf-output">'+esc(n.output)+'</pre></details>':'')+((step.skill_bindings||[]).length?'<details data-wf-d="sk:'+esc(n.node_id)+'"><summary>'+esc(T('wf.skill_snapshot'))+'</summary><ul class="list-plain">'+step.skill_bindings.map(function(b){return '<li>'+esc(b.skill_name||b.skill_id)+' · '+esc(T(b.role==='primary'?'ss.primary':'ss.auxiliary'))+' · <code>'+esc(b.version_id)+'</code></li>';}).join('')+'</ul></details>':'')+'</article>';}).join('')+'</div>','workflows',[[T('nav.workflows'),'#/workflows'],[r.snapshot.name,'#/workflows/'+enc(r.workflow_id)],[T('wf.execution')]]);
      if(keep)wfRunRestore(page(),keep);
      if(!active){stopPoll();return;}
      var here=location.hash,sig=JSON.stringify(r),held=null;
      var repaint=function(next){if(location.hash!==here)return;var pg=page(),y=window.scrollY,sy=pg.scrollTop;paintWorkflowRun(id,next,wfRunKeep(pg));window.scrollTo(0,y);pg.scrollTop=sy;};
      var fetchNext=function(){return api('/api/workflow-runs/'+enc(id));};
      $('#wf-stop').onclick=function(){var b=this;b.disabled=true;api('/api/workflow-runs/'+enc(id)+'/stop',{method:'POST',body:{}}).then(fetchNext).then(repaint).catch(function(e){b.disabled=false;fail(e);});};
      livePoll(function(){
        if(held){if(liveQuiet(page())){var d=held;held=null;repaint(d);}return;}
        return fetchNext().then(function(next){if(location.hash!==here)return;var s=JSON.stringify(next);if(s===sig)return;sig=s;if(liveQuiet(page()))repaint(next);else held=next;});
      },2000);
  }

  function dependencyFindingHTML(items) {
    return (items||[]).map(function(f){var level=['missing','uncertain','available','blocked'].indexOf(f.level)>=0?f.level:'uncertain';
      return '<article class="dep-finding"><div class="row-actions"><strong>'+esc(f.dependency)+'</strong><span class="grow"></span><span class="pill '+(level==='missing'||level==='blocked'?'bad':level==='available'?'ok':'warn')+'">'+esc(T('dep.'+level))+'</span></div><p>'+esc(f.reason)+'</p>'+(f.evidence?'<details><summary>'+esc(T('dep.evidence'))+'</summary><pre class="wf-output">'+esc(f.evidence)+'</pre></details>':'')+(f.suggestion?'<p class="notice">'+esc(f.suggestion)+'</p>':'')+'</article>';
    }).join('');
  }
  function preflightHTML(r) {
    var p=r.runtime_profile||{};
    return '<h3>'+esc(T('dep.runtime'))+'</h3><dl class="kv"><dt>'+esc(T('rn.mode'))+'</dt><dd>'+esc(T('dep.mode_'+p.mode))+'</dd><dt>'+esc(T('rn.folder'))+'</dt><dd>'+esc(p.workspace||T('dep.workspace_'+p.workspace_kind))+'</dd></dl>'+
      (p.denied_tools&&p.denied_tools.length?'<p>'+esc(T('dep.denied'))+' <code>'+esc(p.denied_tools.join(', '))+'</code></p>':'')+
      '<p class="hint">'+esc(p.notes||'')+'</p>'+
      (r.context_usage||[]).map(function(u){return '<details class="ss-details" open><summary>'+esc(T('dep.context'))+' · '+esc(u.origin)+'</summary><p class="notice '+(u.exceeded?'bad':'')+'">'+esc(T('dep.context_size',{kb:(u.total_bytes/1024).toFixed(1),limit:Math.round(u.limit_bytes/1024),tokens:u.estimated_tokens}))+'</p><ul class="list-plain">'+(u.members||[]).map(function(m){return '<li>'+esc(m.name)+' · '+esc(T(m.role==='primary'?'ss.primary':'ss.auxiliary'))+' · '+esc((m.bytes/1024).toFixed(1))+' KiB · ≈ '+esc(m.estimated_tokens)+' tokens</li>';}).join('')+'</ul><p class="hint">'+esc(u.estimate_method)+'</p></details>';}).join('')+
      (r.requirements&&r.requirements.length?'<h3>'+esc(T('sm.environment'))+'</h3>'+r.requirements.map(function(x){return '<p><span class="pill '+(x.status==='missing'||x.status==='blocked'?'bad':x.status==='present'?'ok':'warn')+'">'+esc(T('dep.req_'+x.status))+'</span> '+esc(x.requirement.kind)+' · '+esc(x.requirement.name)+'<br><small>'+esc(x.evidence)+'</small></p>';}).join(''):'')+
      (r.truncated?'<p class="notice warn">'+esc(T('dep.truncated'))+'</p>':'')+dependencyFindingHTML(r.findings);
  }
  // A drawer preserves unsaved forms; free deterministic checks never invoke AI.
  function dependencyCheck(request, localOnly) {
    var current='',generation=0;
    var panel=openDrawer(function(dw){
      dw.innerHTML='<div class="drawer-head"><h2>'+ic('shield','sm')+esc(T('dep.check'))+'</h2><button class="icon-btn" id="dep-close" aria-label="'+esc(T('btn.close'))+'">'+ic('x')+'</button></div><div class="drawer-body"><p class="notice">'+esc(T('dep.scope'))+'</p>'+
        fieldHtml(T('dep.python'),'<input id="dep-python" class="mono" value="'+esc(request.python_environment||'')+'" placeholder="/path/to/.venv">')+
        '<div class="row-actions"><button class="btn" id="dep-local">'+esc(T('dep.local'))+'</button><button class="btn primary" id="dep-ai">'+esc(T('dep.ai'))+'</button></div><div id="dep-preflight" role="status"></div><h3>'+esc(T('dep.ai_result'))+'</h3><div id="dep-result" role="status" aria-live="polite">'+esc(T('dep.local_only'))+'</div>'+
        '<div class="row-actions"><button class="btn hidden" id="dep-stop">'+esc(T('btn.stop'))+'</button><a class="btn hidden" id="dep-log">'+esc(T('wf.open_log'))+'</a></div></div>';
      $('#dep-close',dw).onclick=closeDrawer;
      $('#dep-local',dw).onclick=function(){start(false);};
      $('#dep-ai',dw).onclick=function(){start(true);};
      $('#dep-stop',dw).onclick=function(){var b=this;b.disabled=true;api('/api/runs/'+enc(current)+'/stop',{method:'POST',body:{}}).catch(fail).finally(function(){b.disabled=false;});};
    });
    function busy(on){['dep-local','dep-ai','dep-python'].forEach(function(id){$('#'+id,panel).disabled=on;});}
    function present(r){
      var active=r.status==='queued'||r.status==='running',box=$('#dep-result',panel);
      $('#dep-stop',panel).classList.toggle('hidden',!active);
      var log=$('#dep-log',panel);log.classList.remove('hidden');log.href='#/runs/'+enc(r.child_run_id||current);log.onclick=closeDrawer;
      if(r.preflight)$('#dep-preflight',panel).innerHTML=preflightHTML(r.preflight);
      box.innerHTML=active?'<p>'+ic('spinner','sm')+esc(T('dep.analyzing'))+'</p>':r.report?'<p class="dep-summary">'+esc(r.report.summary)+'</p>'+dependencyFindingHTML(r.report.findings):'<p class="notice bad">'+esc(r.error||T('wf.status_'+r.status))+'</p>';
      if(!active)busy(false);return active;
    }
    function poll(gen){if(gen!==generation||!document.contains(panel))return;api('/api/dependencies/check/'+enc(current)).then(function(r){if(gen!==generation||!document.contains(panel))return;if(present(r))setTimeout(function(){poll(gen);},1500);}).catch(function(e){if(document.contains(panel)){$('#dep-result',panel).textContent=e.message;busy(false);}});}
    function start(ai){
      var gen=++generation,req=Object.assign({},request,{python_environment:$('#dep-python',panel).value.trim()});busy(true);
      $('#dep-preflight',panel).textContent=T('dep.starting');$('#dep-result',panel).textContent=T(ai?'dep.starting':'dep.local_only');
      api('/api/dependencies/preflight',{method:'POST',body:req}).then(function(r){
        if(gen!==generation||!document.contains(panel))return;$('#dep-preflight',panel).innerHTML=preflightHTML(r);
        if(!ai){busy(false);return;}
        return api('/api/dependencies/check',{method:'POST',body:req}).then(function(v){current=v.run_id;poll(gen);});
      }).catch(function(e){if(document.contains(panel)&&gen===generation){$('#dep-result',panel).textContent=e.message;busy(false);}});
    }
    start(!localOnly);
  }


  function workflowContextFields(n) {
    var c=n.context;
    return '<label class="field"><span>'+esc(T('cp.enable'))+'</span><input type="checkbox" id="wf-context-enabled"'+(c?' checked':'')+'></label>'+(c?
      fieldHtml(T('cp.budget'),'<input type="number" min="4097" max="2000000" id="wf-context-budget" value="'+esc((c.budget||{}).max_input_tokens||65536)+'">')+
      fieldHtml(T('cp.mode'),'<select id="wf-context-mode">'+['full','summary','reference'].map(function(mode){return opt(mode,T('cp.mode_'+mode),mode===(c.mode||'full'));}).join('')+'</select>')+
      fieldHtml(T('cp.limit'),'<input type="number" min="256" max="65536" id="wf-context-limit" value="'+esc(c.max_tokens||2048)+'">')+'<p class="hint">'+esc(T('cp.hint'))+'</p>':'');
  }
