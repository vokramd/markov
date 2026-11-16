# CC-71 mutable repository metadata inherits two-day cache lifetime

Observed during approved initial public publication: InRelease, repomd.xml and markov.db return Cache-Control max-age=172800. Bytes/signatures and native clients pass, but this can delay updates and conflicts with the reviewed revalidation policy. InRelease/pacman db also lack explicit Content-Type; repomd.xml.asc inherits a key MIME type.

Native repositories are live at the correct /home/tannerjc/markovd.com/repos tree, 214 current/history hashes verified. Eleven exact public package rows and existing Omarchy VM public updater/upgrade/install/remove/user-data checks passed. Release remains draft and /install/ unchanged while the policy correction is reviewed.

Proposed site-local branch cc-71-repository-cache commit 829ccb6 contains repo-tools/hosting/repos.htaccess and scope/test documentation. Exact file hash 77d5925dd263704ec01d790f839bbbada63b14c8a1b9d07c06ff570dfaf3ef3f, live target /home/tannerjc/markovd.com/repos/.htaccess (currently absent). Local Apache syntax/HTTP test against two-day inherited expiry passed: one no-cache/max-age=0/must-revalidate header, no Expires, expected MIME. No DreamHost config change made.

Specific approval is pending because the reviewed publication checklist explicitly excluded .htaccess changes. Do not route this operator config through inbox/promotion manifests or relax dotfile rejection. Root .htaccess/authorized_keys/site remain at their baselines. After approval push/review/merge the small fix PR, upload only the reviewed file by pinned desktop admin SSH, verify live headers/body/ETag/Range and no errors, rollback only the new matching config if acceptance fails, and only then finish the already-approved Release/site publication.
