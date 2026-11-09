// activeTab is granted only by the toolbar gesture. No history/bookmarks access,
// persistent host permission, remote service, or background page monitoring.
chrome.action.onClicked.addListener(async tab => {
 if(!tab.id||!/^https?:\/\//i.test(tab.url??''))return;
 const [result]=await chrome.scripting.executeScript({target:{tabId:tab.id},func:()=>({url:location.href,title:document.title.slice(0,1024),text:(getSelection()?.toString()||'').slice(0,16000)})});
 const page=result?.result;if(!page||!/^https?:\/\//i.test(page.url))return;
 const payload={schema:'wuu.share.v1',capture_id:crypto.randomUUID(),kind:'current_page',...page};
 const encoded=encodeURIComponent(JSON.stringify(payload));
 await chrome.downloads.download({url:'data:application/json;charset=utf-8,'+encoded,filename:'current-page.wuu-share.json',saveAs:true,conflictAction:'uniquify'});
});
