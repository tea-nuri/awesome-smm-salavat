"""Parse public Telegram previews or a Telegram Desktop JSON export. No login."""
import json
import re
import sys
from html.parser import HTMLParser

class Node:
    def __init__(self, tag='', attrs=()):
        self.tag, self.attrs, self.children = tag, dict(attrs), []
    def has(self, name):
        return name in self.attrs.get('class', '').split()
    def all(self):
        yield self
        for child in self.children:
            if isinstance(child, Node):
                yield from child.all()
    def text(self):
        return ''.join(c.text() if isinstance(c, Node) else c for c in self.children)

class Parser(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.root = Node()
        self.stack = [self.root]
    def handle_starttag(self, tag, attrs):
        node = Node(tag, attrs)
        self.stack[-1].children.append(node)
        if tag == 'br':
            node.children.append('\n')
        if tag not in {'br','img','input','meta','link','hr','source','wbr','area','embed','param','track','col'}:
            self.stack.append(node)
    def handle_startendtag(self, tag, attrs):
        self.handle_starttag(tag, attrs)
        if self.stack[-1].tag == tag:
            self.stack.pop()
    def handle_endtag(self, tag):
        for i in range(len(self.stack)-1, 0, -1):
            if self.stack[i].tag == tag:
                del self.stack[i:]
                break
    def handle_data(self, data):
        self.stack[-1].children.append(data)

def count(text):
    match = re.fullmatch(r'\s*([\d.,\s\u00a0]+)\s*([KMBКМ]?)\s*', text, re.I)
    if not match:
        return None, False
    number = match[1].replace('\u00a0','').replace(' ','').replace(',','.')
    try:
        scale = {'K':1000, 'К':1000, 'M':1000000, 'М':1000000, 'B':1000000000}.get(match[2].upper(), 1)
        return int(float(number)*scale), scale != 1
    except ValueError:
        return None, False

def html_posts(raw):
    parser = Parser()
    parser.feed(raw)
    title = next((n.text().strip() for n in parser.root.all() if n.has('tgme_channel_info_header_title')), '')
    posts = []
    for node in parser.root.all():
        if not node.has('tgme_widget_message') or not node.attrs.get('data-post'):
            continue
        link = node.attrs['data-post']
        if not re.fullmatch(r'[\w]+/\d+', link):
            continue
        children = list(node.all())
        text = next((n.text().strip() for n in children if n.has('tgme_widget_message_text')), '')
        date = next((n.attrs.get('datetime','') for n in children if n.tag == 'time'), '')
        if not date:
            continue
        post = {'id':int(link.split('/')[-1]),'url':'https://t.me/'+link,'text':text[:5000], 'date':date, 'source':'public_preview', 'approximate':False}
        for key, cls in [('views','tgme_widget_message_views'),('comments','tgme_widget_message_replies_count')]:
            found = next((n for n in children if n.has(cls)), None)
            if found:
                value, approximate = count(found.text())
                if value is not None:
                    post[key] = value
                    post[key+'_approximate'] = approximate
        # Stars are paid units, not emoji reactions.
        reactions = [n for n in children if n.has('tgme_reaction') and not n.has('tgme_reaction_paid')]
        values = []
        for n in reactions:
            value, approximate = count(''.join(c for c in n.children if isinstance(c, str)))
            if value is not None:
                values.append(value)
                post['approximate'] |= approximate
        if values:
            post['reactions'] = sum(values)
        posts.append(post)
    return {'name': title, 'posts': posts}

def plain(value):
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return ''.join(plain(v) for v in value)
    if isinstance(value, dict):
        return plain(value.get('text',''))
    return ''

def export_posts(raw):
    data = json.loads(raw)
    posts=[]
    for message in data.get('messages', [])[-1000:]:
        if message.get('type') != 'message' or not isinstance(message.get('id'), int):
            continue
        date = message.get('date','')
        if not date:
            continue
        if not re.search(r'(Z|[+-]\d\d:\d\d)$', date):
            # Desktop exports supply UTC epoch independently of machine timezone.
            from datetime import datetime, timezone
            if message.get('date_unixtime'):
                date=datetime.fromtimestamp(int(message['date_unixtime']), timezone.utc).isoformat()
            else:
                raise ValueError('В выгрузке нет часового пояса или date_unixtime')
        post={'id':message['id'], 'text':plain(message.get('text',''))[:5000], 'url':message.get('link',''), 'date':date, 'source':'telegram_export', 'approximate':False}
        reactions=message.get('reactions')
        if isinstance(reactions, dict):
            reactions=reactions.get('results',[])
        if isinstance(reactions, list):
            counts=[r['count'] for r in reactions if isinstance(r,dict) and isinstance(r.get('count'),int) and r.get('type') not in ('paid','stars')]
            if counts:
                post['reactions']=sum(counts)
        for key in ('views','comments'):
            if isinstance(message.get(key),int) and message[key]>=0:
                post[key]=message[key]
        posts.append(post)
    if not posts:
        raise ValueError('В JSON нет сообщений Telegram Desktop')
    return {'name':data.get('name','Импорт Telegram'), 'posts':posts}

if __name__=='__main__':
    try:
        raw=sys.stdin.read(10*1024*1024+1)
        if len(raw)>10*1024*1024:
            raise ValueError('Файл больше 10 МБ')
        print(json.dumps(export_posts(raw) if len(sys.argv)>1 and sys.argv[1]=='export' else html_posts(raw), ensure_ascii=False))
    except Exception:
        print(json.dumps({'error':'Не удалось разобрать данные Telegram. Нужна публичная лента или JSON-выгрузка Telegram Desktop.'}, ensure_ascii=False))
        sys.exit(1)
