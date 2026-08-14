(() => {
  const ready = () => {
    const widget = document.querySelector('[data-messenger]');
    if (!widget) return;
    const panel = widget.querySelector('[data-messenger-panel]');
    const messages = widget.querySelector('[data-messenger-messages]');
    const form = widget.querySelector('form');
    const input = form.querySelector('textarea');
    const status = widget.querySelector('[data-messenger-status]');
    let channel = 'team';
    let open = false;

    const setStatus = (text) => { status.textContent = text || ''; };
    const render = (items) => {
      messages.replaceChildren();
      if (!items.length) {
        const empty = document.createElement('p'); empty.className = 'messenger-empty';
        empty.textContent = channel === 'team' ? 'Start a conversation with your organization.' : 'Start a conversation with Mainspring Support.';
        messages.append(empty); return;
      }
      for (const item of items) {
        const article = document.createElement('article'); article.className = 'messenger-message';
        const heading = document.createElement('strong'); heading.textContent = item.SenderName;
        const body = document.createElement('p'); body.textContent = item.Body;
        article.append(heading, body); messages.append(article);
      }
      messages.scrollTop = messages.scrollHeight;
    };
    const load = async () => {
      try {
        const response = await fetch('/api/messenger/' + channel, {headers: {'Accept': 'application/json'}});
        if (!response.ok) throw new Error('load');
        render((await response.json()).messages || []); setStatus('');
      } catch (_) { setStatus('Messages are temporarily unavailable.'); }
    };
    widget.querySelector('[data-messenger-toggle]').addEventListener('click', () => {
      open = !open; panel.hidden = !open; if (open) { load(); input.focus(); }
    });
    widget.querySelectorAll('[data-messenger-channel]').forEach((button) => button.addEventListener('click', () => {
      channel = button.dataset.messengerChannel;
      widget.querySelectorAll('[data-messenger-channel]').forEach((item) => item.classList.toggle('is-active', item === button));
      load();
    }));
    form.addEventListener('submit', async (event) => {
      event.preventDefault(); const body = input.value.trim(); if (!body) return;
      setStatus('Sending…');
      try {
        const data = new URLSearchParams({body, csrf_token: widget.dataset.csrf});
        const response = await fetch('/api/messenger/' + channel, {method: 'POST', headers: {'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'application/json'}, body: data});
        if (!response.ok) throw new Error('send'); input.value = ''; await load();
      } catch (_) { setStatus('Your message could not be sent.'); }
    });
    window.setInterval(() => { if (open) load(); }, 10000);
  };
  document.readyState === 'loading' ? document.addEventListener('DOMContentLoaded', ready) : ready();
})();
