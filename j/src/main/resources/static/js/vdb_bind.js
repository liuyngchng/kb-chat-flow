// 业务知识库绑定页 JS（独立页面，供管理员配置客服流程各分支使用的知识库）

// 认证走 httpOnly Cookie，fetch 自动携带，无需手动附加 token
function authFetch(url, options) {
    return fetch(url, options);
}

// 绑定下拉框与配置 key 的映射
var BIND_FIELDS = [
    { select: 'billingBindSelect', cfgKey: 'billing', label: '账单' },
    { select: 'repairBindSelect', cfgKey: 'repair', label: '维修' },
    { select: 'faqBindSelect', cfgKey: 'faq', label: 'FAQ' }
];

// 加载绑定配置并回显
async function loadBindings() {
    try {
        var resp = await authFetch('/api/v1/vdb/bindings');
        if (resp.status === 403 || resp.status === 401) {
            alert('无权访问：仅管理员可配置知识库绑定');
            return;
        }
        var data = await resp.json();
        var bindings = data.data || {};

        var kbResp = await authFetch('/api/v1/vdb');
        var kbData = await kbResp.json();
        var kbs = kbData.data || [];

        BIND_FIELDS.forEach(function(f) {
            var sel = document.getElementById(f.select);
            var placeholder = sel.querySelector('option[value=""]');
            sel.innerHTML = '';
            if (placeholder) sel.appendChild(placeholder);

            kbs.forEach(function(kb) {
                var opt = document.createElement('option');
                opt.value = kb.id;
                opt.textContent = kb.name;
                sel.appendChild(opt);
            });

            var ids = (bindings[f.cfgKey] || []).map(String);
            Array.from(sel.options).forEach(function(opt) {
                opt.selected = opt.value !== '' && ids.indexOf(opt.value) !== -1;
            });
        });
    } catch (e) {
        console.error('加载绑定失败:', e);
        alert('加载绑定配置失败');
    }
}

// 保存绑定
async function saveBindings() {
    var payload = {};
    BIND_FIELDS.forEach(function(f) {
        var sel = document.getElementById(f.select);
        payload[f.cfgKey] = Array.from(sel.selectedOptions).map(function(o) { return parseInt(o.value); });
    });

    try {
        var resp = await authFetch('/api/v1/vdb/bindings', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        var data = await resp.json();
        if (data.status === 'ok') {
            var statusEl = document.getElementById('bind_status');
            statusEl.style.display = 'inline-block';
            statusEl.style.animation = 'none';
            statusEl.offsetHeight;
            statusEl.style.animation = 'toastFade 3s ease-out forwards';
            setTimeout(function() { statusEl.style.display = 'none'; }, 3000);
        } else {
            alert(data.error || '保存失败');
        }
    } catch (e) {
        console.error('保存绑定失败:', e);
        alert('保存失败');
    }
}

document.getElementById('saveBindingsBtn').addEventListener('click', saveBindings);
document.getElementById('refreshBindingsBtn').addEventListener('click', loadBindings);

loadBindings();
