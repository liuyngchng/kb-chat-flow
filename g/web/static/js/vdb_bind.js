// 业务知识库绑定页 JS（独立页面，供管理员配置客服流程各分支使用的知识库）

// 浏览器请求通过 httpOnly Cookie 自动携带 token，无需手动管理
function authFetch(url, options) {
    return fetch(url, options);
}

// 绑定下拉框与配置 key 的映射
const BIND_FIELDS = [
    { select: 'billingBindSelect', cfgKey: 'billing', label: '账单' },
    { select: 'repairBindSelect', cfgKey: 'repair', label: '维修' },
    { select: 'faqBindSelect', cfgKey: 'faq', label: 'FAQ' }
];

// 加载绑定配置并回显
async function loadBindings() {
    try {
        // 读取当前绑定
        const resp = await authFetch('/api/v1/vdb/bindings');
        if (resp.status === 403 || resp.status === 401) {
            alert('无权访问：仅管理员可配置知识库绑定');
            return;
        }
        const data = await resp.json();
        const bindings = data.data || {};

        // 用知识库列表填充三个多选下拉
        const kbResp = await authFetch('/api/v1/vdb');
        const kbData = await kbResp.json();
        const kbs = kbData.data || [];

        BIND_FIELDS.forEach(f => {
            const sel = document.getElementById(f.select);
            // 保留提示项
            const placeholder = sel.querySelector('option[value=""]');
            sel.innerHTML = '';
            if (placeholder) sel.appendChild(placeholder);

            kbs.forEach(kb => {
                const opt = document.createElement('option');
                opt.value = kb.id;
                opt.textContent = kb.name;
                sel.appendChild(opt);
            });

            // 回显当前绑定
            const ids = (bindings[f.cfgKey] || []).map(String);
            Array.from(sel.options).forEach(opt => {
                opt.selected = opt.value !== '' && ids.includes(opt.value);
            });
        });
    } catch (e) {
        console.error('加载绑定失败:', e);
        alert('加载绑定配置失败');
    }
}

// 保存绑定
async function saveBindings() {
    const payload = {};
    BIND_FIELDS.forEach(f => {
        const sel = document.getElementById(f.select);
        payload[f.cfgKey] = Array.from(sel.selectedOptions).map(o => parseInt(o.value));
    });

    try {
        const resp = await authFetch('/api/v1/vdb/bindings', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        const data = await resp.json();
        if (data.status === 'ok') {
            const statusEl = document.getElementById('bind_status');
            statusEl.style.display = 'inline-block';
            statusEl.style.animation = 'none';
            statusEl.offsetHeight;
            statusEl.style.animation = 'toastFade 3s ease-out forwards';
            setTimeout(() => { statusEl.style.display = 'none'; }, 3000);
        } else {
            alert(data.error || '保存失败');
        }
    } catch (e) {
        console.error('保存绑定失败:', e);
        alert('保存失败');
    }
}

// 事件绑定
document.getElementById('saveBindingsBtn').addEventListener('click', saveBindings);
document.getElementById('refreshBindingsBtn').addEventListener('click', loadBindings);

// 页面初始化
loadBindings();